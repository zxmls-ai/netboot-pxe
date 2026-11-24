package mobilecore

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/go-filesystems/iso9660"
)

// Preparation lets the app cancel a long ISO extraction when PXE stops.
type Preparation interface {
	Cancelled() bool
}

// PrepareISO reads an ISO 9660 volume in the unprivileged application process.
// Both output directories are private staging folders created by the app.
func PrepareISO(imagePath, bootDir, sourceDir string, progress Preparation) (string, error) {
	image, err := os.Open(imagePath)
	if err != nil {
		return "", err
	}
	defer image.Close()
	info, err := image.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("media_not_regular")
	}
	volume, err := iso9660.Open(image, info.Size())
	if err != nil {
		return "unsupported", nil
	}
	if progress == nil {
		return "", errors.New("media_callback_failed")
	}
	files := make(map[string]string)
	scanned := 0
	var scan func(string, int) error
	scan = func(dir string, depth int) error {
		if progress.Cancelled() {
			return errors.New("media_cancelled")
		}
		if depth > 32 {
			return errors.New("media_invalid_iso")
		}
		children, err := volume.ListDir(dir)
		if err != nil {
			return err
		}
		for _, child := range children {
			scanned++
			name := child.Name()
			if name == "." || name == ".." || name == "" || strings.ContainsAny(name, "/\\:\x00") || len(name) > 255 || scanned > 100000 {
				return errors.New("media_invalid_iso")
			}
			key := path.Join(dir, name)
			if child.FileType() == 2 {
				if err := scan(key, depth+1); err != nil {
					return err
				}
			} else {
				folded := strings.ToLower(key)
				if existing := files[folded]; existing != "" && existing != key {
					return errors.New("media_invalid_iso")
				}
				files[folded] = key
			}
		}
		return nil
	}
	if err := scan("/", 0); err != nil {
		return "", err
	}
	pick := func(names ...string) string {
		for _, name := range names {
			if actual := files[name]; actual != "" {
				return actual
			}
		}
		return ""
	}
	buffer := make([]byte, 256*1024)
	copyFile := func(name, target string) error {
		if name == "" {
			return nil
		}
		input, err := volume.OpenFile(name)
		if err != nil {
			return err
		}
		defer input.Close()
		reader := io.NewSectionReader(input, 0, input.Size())
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		defer output.Close()
		var lastCheck time.Time
		for {
			if time.Since(lastCheck) >= 100*time.Millisecond {
				if progress.Cancelled() {
					return errors.New("media_cancelled")
				}
				lastCheck = time.Now()
			}
			n, readErr := reader.Read(buffer)
			if n > 0 {
				if _, err := output.Write(buffer[:n]); err != nil {
					return err
				}
			}
			if readErr == io.EOF {
				return output.Close()
			}
			if readErr != nil {
				return readErr
			}
			if n == 0 {
				return io.ErrNoProgress
			}
		}
	}
	loader := pick("/efi/boot/bootx64.efi")
	bcd := pick("/efi/microsoft/boot/bcd", "/boot/bcd")
	sdi := pick("/boot/boot.sdi", "/sources/boot.sdi")
	wim := pick("/sources/boot.wim")
	installing := pick("/setup.exe") != "" && pick("/sources/install.wim", "/sources/install.esd", "/sources/install.swm") != ""
	if loader != "" && bcd != "" {
		if wim == "" && !installing {
			var candidates []string
			for key, actual := range files {
				if strings.HasSuffix(key, ".wim") && !strings.HasPrefix(path.Base(key), "install") {
					candidates = append(candidates, actual)
				}
			}
			wim, err = selectPEWim(volume, bcd, candidates)
			if err != nil {
				return "", err
			}
		}
		if wim != "" {
			f, err := volume.OpenFile(wim)
			if err != nil {
				return "", err
			}
			var signature [8]byte
			_, err = f.ReadAt(signature[:], 0)
			f.Close()
			if err != nil || string(signature[:]) != "MSWIM\x00\x00\x00" {
				return "unsupported", nil
			}
			if installing {
				for name, actual := range files {
					if err := copyFile(actual, filepath.Join(sourceDir, filepath.FromSlash(strings.TrimPrefix(name, "/")))); err != nil {
						return "", err
					}
				}
				return "windows", nil
			}
			if err := copyFile(wim, filepath.Join(bootDir, "boot.wim")); err != nil {
				return "", err
			}
			for name, actual := range map[string]string{"BCD": bcd, "boot.sdi": sdi} {
				if err := copyFile(actual, filepath.Join(bootDir, name)); err != nil {
					return "", err
				}
			}
			return "winpe", nil
		}
	}
	kind := "casper"
	kernel := pick("/casper/vmlinuz", "/casper/vmlinuz.efi")
	initrd := pick("/casper/initrd", "/casper/initrd.lz", "/casper/initrd.gz")
	if kernel == "" || initrd == "" {
		kernel = pick("/install.amd/vmlinuz")
		initrd = pick("/install.amd/gtk/initrd.gz", "/install.amd/initrd.gz")
		kind = "debian"
		if pick("/trimfs.tgz") != "" && pick("/config/trimfs.conf") != "" {
			kind = "fnos"
		}
	}
	if loader == "" || kernel == "" || initrd == "" {
		return "unsupported", nil
	}
	if err := copyFile(kernel, filepath.Join(bootDir, "vmlinuz")); err != nil {
		return "", fmt.Errorf("copy kernel: %w", err)
	}
	if err := copyFile(initrd, filepath.Join(bootDir, "initrd")); err != nil {
		return "", fmt.Errorf("copy initrd: %w", err)
	}
	return kind, nil
}

// Use the RAM-disk path in the EFI BCD when the disc contains multiple PE WIMs.
func selectPEWim(volume *iso9660.FS, bcd string, candidates []string) (string, error) {
	if len(candidates) == 0 {
		return "", nil
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	store, err := volume.OpenFile(bcd)
	if err != nil {
		return "", err
	}
	defer store.Close()
	data, err := io.ReadAll(io.NewSectionReader(store, 0, min(store.Size(), 2<<20)))
	if err != nil {
		return "", err
	}
	if len(data) < 2 {
		return "", nil
	}
	match := ""
	for offset := range 2 {
		codeUnits := make([]uint16, (len(data)-offset)/2)
		for i := range codeUnits {
			codeUnits[i] = binary.LittleEndian.Uint16(data[offset+i*2:])
		}
		paths := strings.ReplaceAll(strings.ToLower(string(utf16.Decode(codeUnits))), "\\", "/")
		for _, image := range candidates {
			if strings.Contains(paths, strings.ToLower(image)+"\x00") {
				if match != "" && match != image {
					return "", nil
				}
				match = image
			}
		}
	}
	return match, nil
}
