package mobilecore

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-filesystems/iso9660"
)

type testPreparation bool

func (p testPreparation) Cancelled() bool { return bool(p) }

func TestPrepareISO(t *testing.T) {
	for _, test := range []struct {
		name  string
		files map[string]string
		boot  map[string]string
		share map[string]string
	}{
		{
			name: "casper",
			files: map[string]string{
				"/CASPER/VMLINUZ": "kernel", "/CASPER/INITRD": "ramdisk",
			},
			boot: map[string]string{"vmlinuz": "kernel", "initrd": "ramdisk"},
		},
		{
			name: "windows",
			files: map[string]string{
				"/SETUP.EXE": "setup", "/SOURCES/BOOT.WIM": "MSWIM\x00\x00\x00",
				"/SOURCES/INSTALL.ESD": "installation", "/BOOT/BCD": "bcd", "/BOOT/BOOT.SDI": "sdi",
			},
			share: map[string]string{"setup.exe": "setup", "sources/install.esd": "installation"},
		},
		{
			name: "winpe",
			files: map[string]string{
				"/PE/WINPE.WIM": "MSWIM\x00\x00\x00", "/PE/OTHER.WIM": "MSWIM\x00\x00\x00other",
				"/BOOT/BCD": "\\\x00P\x00E\x00\\\x00W\x00I\x00N\x00P\x00E\x00.\x00W\x00I\x00M\x00\x00\x00",
			},
			boot: map[string]string{"boot.wim": "MSWIM\x00\x00\x00"},
		},
		{name: "unsupported", files: map[string]string{"/README.TXT": "data"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			image, err := os.CreateTemp(t.TempDir(), "*.iso")
			if err != nil {
				t.Fatal(err)
			}
			defer image.Close()
			builder := iso9660.NewBuilder("TEST")
			if err := builder.AddFile("/EFI/BOOT/BOOTX64.EFI", []byte("efi")); err != nil {
				t.Fatal(err)
			}
			for name, value := range test.files {
				if err := builder.AddFile(name, []byte(value)); err != nil {
					t.Fatal(err)
				}
			}
			if err := builder.WriteTo(image); err != nil {
				t.Fatal(err)
			}
			boot, share := t.TempDir(), t.TempDir()
			if _, err := PrepareISO(image.Name(), boot, share, testPreparation(true)); err == nil {
				t.Fatal("cancelled preparation continued")
			}
			kind, err := PrepareISO(image.Name(), boot, share, testPreparation(false))
			if err != nil || kind != test.name {
				t.Fatalf("PrepareISO = %q, %v", kind, err)
			}
			for root, outputs := range map[string]map[string]string{boot: test.boot, share: test.share} {
				for name, expected := range outputs {
					got, err := os.ReadFile(filepath.Join(root, name))
					if err != nil || string(got) != expected {
						t.Errorf("output %s = %q, %v", name, got, err)
					}
				}
			}
		})
	}
}
