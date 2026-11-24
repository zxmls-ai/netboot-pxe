package mobilecore

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-filesystems/osfs"
	"github.com/go-filesystems/smb"
	"github.com/sky22333/android-netboot/core-go/internal/isodisk"
)

type server struct {
	cfg       config
	sink      *eventSink
	cancel    context.CancelFunc
	startedAt time.Time
	wg        sync.WaitGroup
	http      *http.Server
	httpLn    net.Listener
	isoDiskLn net.Listener
	smbLn     net.Listener
	smb       *smb.Server
	windowsFS *osfs.FS
	udp       []net.PacketConn
	leases    *leasePool
}

func newServer(cfg config, listener Listener) *server {
	return &server{cfg: cfg, sink: &eventSink{listener: listener}, leases: newLeasePool(cfg.DHCP)}
}

func (s *server) start() error {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	httpLn, err := net.Listen("tcp4", net.JoinHostPort(s.cfg.ListenIP, fmt.Sprint(s.cfg.HTTPPort)))
	if err != nil {
		cancel()
		return fmt.Errorf("listen HTTP: %w", err)
	}
	s.httpLn = httpLn
	if s.cfg.ISODiskPort != 0 {
		s.isoDiskLn, err = net.Listen("tcp4", net.JoinHostPort(s.cfg.ListenIP, fmt.Sprint(s.cfg.ISODiskPort)))
		if err != nil {
			s.closeListeners()
			cancel()
			return fmt.Errorf("listen ISO disk: %w", err)
		}
	}
	tftpConn, err := net.ListenPacket("udp4", net.JoinHostPort(s.cfg.ListenIP, "69"))
	if err != nil {
		s.closeListeners()
		cancel()
		return fmt.Errorf("listen TFTP: %w", err)
	}
	s.udp = append(s.udp, tftpConn)
	dhcpPorts := []string{"67", "4011"}
	for _, port := range dhcpPorts {
		conn, listenErr := listenDHCPInterface(s.cfg.ListenIP, port)
		if listenErr != nil {
			s.closeListeners()
			cancel()
			return fmt.Errorf("listen DHCP port %s: %w", port, listenErr)
		}
		s.udp = append(s.udp, conn)
	}

	if s.cfg.SMBPassword != "" {
		if err := s.openWindowsShare(); err != nil {
			s.closeListeners()
			cancel()
			return err
		}
	}
	s.startedAt = time.Now()
	s.http = newHTTPServer(s.cfg, s.sink)
	s.http.ConnState = s.httpConnectionState
	if s.isoDiskLn != nil {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			catalog := "nbiso"
			images := ""
			if s.cfg.SMBPassword != "" {
				catalog = "iso-menu"
				images = filepath.Join(filepath.Dir(s.cfg.Root), "iso")
			}
			if err := isodisk.Serve(ctx, s.isoDiskLn, filepath.Join(s.cfg.Root, catalog, "items"), images, 8); err != nil {
				s.sink.emit("error", "runtime", "iso_disk_failed", map[string]string{"error": err.Error()})
			}
		}()
	}
	if s.smbLn != nil {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			if err := s.smb.Serve(s.smbLn); err != nil && !errors.Is(err, net.ErrClosed) && ctx.Err() == nil {
				s.sink.emit("error", "runtime", "smb_failed", map[string]string{"error": err.Error()})
			}
		}()
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if serveErr := s.http.Serve(httpLn); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			s.sink.emit("error", "http", "http_failed", map[string]string{"error": serveErr.Error()})
		}
	}()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		serveTFTP(ctx, tftpConn, s.cfg, s.sink)
	}()
	for index, conn := range s.udp[1:] {
		port := dhcpPorts[index]
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			serveDHCP(ctx, conn, s.cfg, s.leases, s.sink, port)
		}()
	}
	s.sink.emit("info", "runtime", "netboot_started", map[string]string{"mode": s.cfg.Mode})
	return nil
}

func (s *server) openWindowsShare() error {
	root := filepath.Join(filepath.Dir(s.cfg.Root), "pxe-source")
	dir, err := osfs.Open(root, osfs.ReadOnly(), osfs.MaxReadFile(8<<20))
	if err != nil {
		return fmt.Errorf("open Windows installation source: %w", err)
	}
	server := smb.New()
	server.SetName("NETBOOT")
	server.RequireSigning = true
	server.MaxConnections = 16
	server.MaxInFlightTotal = 32
	server.AddUser("nbread", s.cfg.SMBPassword)
	if err := server.Share("install", dir, smb.ReadOnly(), smb.AllowUsers("nbread")); err != nil {
		_ = dir.Close()
		return fmt.Errorf("share Windows installation source: %w", err)
	}
	ln, err := net.Listen("tcp4", net.JoinHostPort(s.cfg.ListenIP, "445"))
	if err != nil {
		_ = dir.Close()
		return fmt.Errorf("listen SMB: %w", err)
	}
	s.smbLn, s.smb, s.windowsFS = ln, server, dir
	return nil
}

// Count connections until their handlers return, not just until Serve exits.
func (s *server) httpConnectionState(_ net.Conn, state http.ConnState) {
	switch state {
	case http.StateNew:
		s.wg.Add(1)
	case http.StateClosed, http.StateHijacked:
		s.wg.Done()
	}
}

func (s *server) stop(timeout time.Duration) error {
	if s.cancel != nil {
		s.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var closeErr error
	if s.http != nil {
		// Explicit stop also ends active boot downloads.
		closeErr = s.http.Close()
	}
	s.closeListeners()
	if s.smb != nil {
		_ = s.smb.Close()
		_ = s.windowsFS.Close()
	}
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		s.sink.emit("info", "runtime", "netboot_stopped", nil)
		s.sink.clear()
		return closeErr
	case <-ctx.Done():
		s.sink.clear()
		return errors.New("timed out stopping netboot")
	}
}

func (s *server) closeListeners() {
	if s.smbLn != nil {
		_ = s.smbLn.Close()
	}
	if s.isoDiskLn != nil {
		_ = s.isoDiskLn.Close()
	}
	if s.httpLn != nil {
		_ = s.httpLn.Close()
	}
	for _, conn := range s.udp {
		_ = conn.Close()
	}
}
