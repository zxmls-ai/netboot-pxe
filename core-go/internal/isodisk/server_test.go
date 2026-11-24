package isodisk

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNamedISO(t *testing.T) {
	const name = "faabcf33ae53976d2b8207a001ff32f4"
	root := t.TempDir()
	dir := filepath.Join(root, name)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	contents := bytes.Repeat([]byte("test"), 1024)
	images := t.TempDir()
	const iso = "12345678-1234-1234-1234-123456789abc.iso"
	if err := os.WriteFile(filepath.Join(dir, "iso-file"), []byte(iso), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(images, iso)
	if err := os.WriteFile(path, contents, 0600); err != nil {
		t.Fatal(err)
	}

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stopped := make(chan error, 1)
	go func() { stopped <- Serve(ctx, ln, root, images, 4) }()

	connect := func(t *testing.T, flags uint32) net.Conn {
		t.Helper()
		conn, err := net.DialTimeout("tcp4", ln.Addr().String(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		var magic [18]byte
		if _, err := io.ReadFull(conn, magic[:]); err != nil || string(magic[:8]) != "NBDMAGIC" {
			t.Fatalf("read greeting: %x, %v", magic, err)
		}
		if err := send(conn, flags); err != nil {
			t.Fatal(err)
		}
		return conn
	}

	t.Run("export name preserves the original bytes and refuses writes", func(t *testing.T) {
		conn := connect(t, 0)
		if err := send(conn, optionMagic, uint32(optExportName), uint32(len(name)), []byte(name)); err != nil {
			t.Fatal(err)
		}
		var export struct {
			Size  uint64
			Flags uint16
		}
		if err := binary.Read(conn, binary.BigEndian, &export); err != nil || export.Size != 4096 || export.Flags != readOnly {
			t.Fatalf("unexpected export: %+v, %v", export, err)
		}
		padding := make([]byte, 124)
		if _, err := io.ReadFull(conn, padding); err != nil || !bytes.Equal(padding, make([]byte, 124)) {
			t.Fatalf("legacy client padding: %x, %v", padding, err)
		}
		request := func(command uint16, offset uint64, size uint32, wantStatus uint32, data []byte) {
			t.Helper()
			if err := send(conn, requestMagic, uint16(0), command, uint64(42), offset, size, data); err != nil {
				t.Fatal(err)
			}
			var reply struct {
				Magic  uint32
				Status uint32
				Cookie uint64
			}
			if err := binary.Read(conn, binary.BigEndian, &reply); err != nil || reply.Magic != simpleReply ||
				reply.Cookie != 42 || reply.Status != wantStatus {
				t.Fatalf("unexpected command reply: %+v, %v", reply, err)
			}
		}
		request(1, 0, 4, 30, []byte("FAIL"))
		request(0, 4094, 4, 22, nil)
		request(0, 0, 2048, 0, nil)
		got := make([]byte, 2048)
		if _, err := io.ReadFull(conn, got); err != nil || !bytes.Equal(got, contents[:2048]) {
			t.Fatalf("read ISO: %v", err)
		}
	})

	t.Run("unknown export is rejected", func(t *testing.T) {
		conn := connect(t, 3)
		// This is a protocol-level name, never a filesystem path.
		name := []byte("../media.iso")
		if err := send(conn, optionMagic, uint32(optExportName), uint32(len(name)), name); err != nil {
			t.Fatal(err)
		}
		reply := make([]byte, 10)
		if _, err := io.ReadFull(conn, reply); err != io.EOF {
			t.Fatalf("expected EOF, got %v", err)
		}
	})

	_ = connect(t, 3)
	stop()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop with a connected client")
	}
}
