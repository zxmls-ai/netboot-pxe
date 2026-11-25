// Package isodisk serves catalog ISO files as read-only NBD exports.
package isodisk

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

const (
	optionMagic    uint64 = 0x49484156454f5054
	optionReply    uint64 = 0x0003e889045565a9
	requestMagic   uint32 = 0x25609513
	simpleReply    uint32 = 0x67446698
	optExportName         = 1
	optAbort              = 2
	optInfo               = 6
	optGo                 = 7
	replyAck              = 1
	replyInfo             = 3
	errUnsupported        = 0x80000001
	errInvalid            = 0x80000003
	errUnknown            = 0x80000006
	readOnly       uint16 = 3 // NBD_FLAG_HAS_FLAGS | NBD_FLAG_READ_ONLY
	maxRead               = 8 << 20
	// Keep the ISO signatures out of the raw NBD device's probe offsets.
	loopOffset = 1 << 20
)

var exportName = regexp.MustCompile("^[a-f0-9]{32}$")
var imageName = regexp.MustCompile(`^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}\.iso$`)

type exportCatalog struct {
	items  string
	images string
}

type isoExport struct {
	file   *os.File
	size   uint64
	offset uint64
}

func (e isoExport) readAt(dst []byte, offset uint64) error {
	if offset < e.offset {
		padding := min(uint64(len(dst)), e.offset-offset)
		clear(dst[:padding])
		dst = dst[padding:]
		offset += padding
	}
	if len(dst) == 0 {
		return nil
	}
	_, err := e.file.ReadAt(dst, int64(offset-e.offset))
	return err
}

// Serve owns the listener and all accepted connections. The catalog root
// contains one directory per ISO, named by the catalog's image ID.
func Serve(ctx context.Context, ln net.Listener, items, images string, limit int) error {
	catalog := exportCatalog{items: items, images: images}
	defer ln.Close()
	stop := context.AfterFunc(ctx, func() { _ = ln.Close() })
	defer stop()
	slots := make(chan struct{}, limit)
	var sessions sync.WaitGroup
	defer sessions.Wait()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		select {
		case slots <- struct{}{}:
			sessions.Add(1)
			go func() {
				defer sessions.Done()
				defer func() { <-slots }()
				defer conn.Close()
				stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
				defer stop()
				serveConn(conn, catalog)
			}()
		default:
			_ = conn.Close()
		}
	}
}

func openExport(catalog exportCatalog, name []byte) isoExport {
	name, loop := bytes.CutSuffix(name, []byte("-loop"))
	file, size := openCatalogISO(catalog, name)
	var offset uint64
	if file != nil && loop {
		offset = loopOffset
	}
	return isoExport{file: file, size: size + offset, offset: offset}
}

func openCatalogISO(catalog exportCatalog, name []byte) (*os.File, uint64) {
	if !exportName.Match(name) {
		return nil, 0
	}
	root, err := os.OpenRoot(catalog.items)
	if err != nil {
		return nil, 0
	}
	defer root.Close()
	dir, err := root.Lstat(string(name))
	if err != nil || !dir.IsDir() {
		return nil, 0
	}
	if catalog.images != "" {
		ref := filepath.Join(string(name), "iso-file")
		const nameLength = 40 // UUID followed by ".iso"
		info, err := root.Lstat(ref)
		if err != nil || !info.Mode().IsRegular() || info.Size() != nameLength {
			return nil, 0
		}
		file, err := root.Open(ref)
		if err != nil {
			return nil, 0
		}
		var name [nameLength]byte
		_, err = io.ReadFull(file, name[:])
		_ = file.Close()
		if err != nil || !imageName.Match(name[:]) {
			return nil, 0
		}
		images, err := os.OpenRoot(catalog.images)
		if err != nil {
			return nil, 0
		}
		defer images.Close()
		return openISO(images, string(name[:]))
	}
	return openISO(root, filepath.Join(string(name), "media.iso"))
}

func openISO(root *os.Root, path string) (*os.File, uint64) {
	before, err := root.Lstat(path)
	if err != nil || !before.Mode().IsRegular() {
		return nil, 0
	}
	file, err := root.Open(path)
	if err != nil {
		return nil, 0
	}
	info, err := file.Stat()
	if err != nil || !os.SameFile(info, before) || info.Size() <= 0 || info.Size()%2048 != 0 {
		_ = file.Close()
		return nil, 0
	}
	return file, uint64(info.Size())
}

func send(w io.Writer, values ...any) error {
	var data bytes.Buffer
	for _, value := range values {
		if err := binary.Write(&data, binary.BigEndian, value); err != nil {
			return err
		}
	}
	_, err := w.Write(data.Bytes())
	return err
}

func optionResult(conn net.Conn, option, kind uint32, payload []byte) error {
	return send(conn, optionReply, option, kind, uint32(len(payload)), payload)
}

func serveConn(conn net.Conn, catalog exportCatalog) {
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	if send(conn, []byte("NBDMAGIC"), optionMagic, uint16(3)) != nil {
		return
	}
	var flags uint32
	if binary.Read(conn, binary.BigEndian, &flags) != nil || flags&^uint32(3) != 0 {
		return
	}
	for {
		var option struct {
			Magic  uint64
			Type   uint32
			Length uint32
		}
		if binary.Read(conn, binary.BigEndian, &option) != nil || option.Magic != optionMagic || option.Length > 4096 {
			return
		}
		payload := make([]byte, option.Length)
		if _, err := io.ReadFull(conn, payload); err != nil {
			return
		}
		// BusyBox negotiates the original newstyle EXPORT_NAME handshake.
		if flags&1 == 0 && option.Type != optExportName {
			return
		}
		switch option.Type {
		case optAbort:
			_ = optionResult(conn, option.Type, replyAck, nil)
			return
		case optExportName:
			export := openExport(catalog, payload)
			if export.file == nil {
				return
			}
			defer export.file.Close()
			var padding []byte
			if flags&2 == 0 {
				padding = make([]byte, 124)
			}
			if send(conn, export.size, readOnly, padding) == nil {
				transmit(conn, export)
			}
			return
		case optInfo, optGo:
			export, err := describe(conn, catalog, option.Type, payload)
			if err != nil {
				return
			}
			if export.file != nil {
				defer export.file.Close()
				transmit(conn, export)
				return
			}
		default:
			if optionResult(conn, option.Type, errUnsupported, nil) != nil {
				return
			}
		}
	}
}

func describe(conn net.Conn, catalog exportCatalog, option uint32, payload []byte) (isoExport, error) {
	if len(payload) < 6 {
		return isoExport{}, optionResult(conn, option, errInvalid, nil)
	}
	length := binary.BigEndian.Uint32(payload)
	if length > uint32(len(payload)-6) {
		return isoExport{}, optionResult(conn, option, errInvalid, nil)
	}
	rest := payload[4+length:]
	count := int(binary.BigEndian.Uint16(rest))
	if len(rest) != 2+2*count {
		return isoExport{}, optionResult(conn, option, errInvalid, nil)
	}
	disk := openExport(catalog, payload[4:4+length])
	if disk.file == nil {
		return isoExport{}, optionResult(conn, option, errUnknown, nil)
	}
	var export [12]byte // NBD_INFO_EXPORT
	binary.BigEndian.PutUint64(export[2:], disk.size)
	binary.BigEndian.PutUint16(export[10:], readOnly)
	err := optionResult(conn, option, replyInfo, export[:])
	// ISO9660 requires the client to use logical sectors of at most 2048 bytes.
	var blocks [14]byte // NBD_INFO_BLOCK_SIZE
	binary.BigEndian.PutUint16(blocks[:], 3)
	binary.BigEndian.PutUint32(blocks[2:], 1)
	binary.BigEndian.PutUint32(blocks[6:], 2048)
	binary.BigEndian.PutUint32(blocks[10:], maxRead)
	if err == nil {
		err = optionResult(conn, option, replyInfo, blocks[:])
	}
	if err == nil {
		err = optionResult(conn, option, replyAck, nil)
	}
	if err != nil || option == optInfo {
		_ = disk.file.Close()
		return isoExport{}, err
	}
	return disk, nil
}

func transmit(conn net.Conn, export isoExport) {
	// Installers can remain idle at a confirmation screen. Cancellation still
	// closes the connection immediately when PXE stops.
	_ = conn.SetDeadline(time.Time{})
	var data []byte
	for {
		var req struct {
			Magic  uint32
			Flags  uint16
			Type   uint16
			Cookie uint64
			Offset uint64
			Length uint32
		}
		if binary.Read(conn, binary.BigEndian, &req) != nil || req.Magic != requestMagic {
			return
		}
		if req.Type == 2 { // disconnect has no reply
			return
		}
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
		var status uint32
		switch {
		case req.Type == 1: // Consume write data before replying EROFS.
			if req.Length > maxRead {
				return
			}
			if _, err := io.CopyN(io.Discard, conn, int64(req.Length)); err != nil {
				return
			}
			status = 30
		case req.Type != 0 || req.Flags != 0 || req.Length > maxRead ||
			req.Offset > export.size || uint64(req.Length) > export.size-req.Offset:
			status = 22
		default:
			if cap(data) < int(req.Length) {
				data = make([]byte, req.Length)
			}
			data = data[:req.Length]
			if err := export.readAt(data, req.Offset); err != nil {
				status = 5
			}
		}
		if send(conn, simpleReply, status, req.Cookie) != nil {
			return
		}
		if status == 0 {
			if _, err := conn.Write(data); err != nil {
				return
			}
		}
		_ = conn.SetDeadline(time.Time{})
	}
}
