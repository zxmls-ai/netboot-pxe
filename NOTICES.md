# Third-party notices

## USB media libraries

`app/src/main/cpp/dependencies.cmake` pins source archive URLs and SHA-256 values;
`app/src/main/cpp/CMakeLists.txt` defines build options and source patches:

- [wimlib 1.14.5](https://wimlib.net/): library-only build without ntfs-3g; LGPL-2.1-or-later. No upstream source edits; the build omits unused platform/API sources.
- [libudfread 1.2.0](https://code.videolan.org/videolan/libudfread): LGPL-2.1-or-later. The build applies a one-line fix selecting the existing `-1` partition discovery mode rather than physical partition zero. Partition-map validation remains unchanged. This supports CDIMAGE volumes with nonzero physical partition identifiers.
- [FatFs R0.16](https://elm-chan.org/fsw/ff/): ChaN's redistribution license, with the published R0.16 patch-1/patch-2 corrections and application-specific configuration, all recorded in CMake.

## PXE and ISO library

- [iPXE snponly](https://github.com/ipxe/ipxe/tree/5e6ea) and [wimboot 2.9.0](https://github.com/ipxe/wimboot/tree/v2.9.0) run on the PXE client.
- [BusyBox static for Ubuntu, 1:1.36.1-6ubuntu3.1](https://launchpad.net/ubuntu/+source/busybox/1%3A1.36.1-6ubuntu3.1) runs in the client's Linux initramfs.
- The Go core links [ISO 9660](https://github.com/go-filesystems/iso9660/tree/v0.4.0), [OSFS](https://github.com/go-filesystems/osfs/tree/v0.4.0) and [SMB](https://github.com/go-filesystems/smb/tree/v0.16.1) from go-filesystems. See the notices and licenses bundled in `app/src/main/assets/licenses/`.
