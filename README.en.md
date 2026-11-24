# NetBoot-PXE

[简体中文](README.md) | English

Turn an Android phone with root permission into a **PXE boot server** or a **read-only USB boot device**.
Download Windows ISOs from Microsoft, import local images, and pause and resume downloads.

## Requirements

- Android 8.0 or later, with permission from your root manager for this app to use `su`. The app checks existing permissions on startup; it does not install or patch root software.
- PXE: connect the phone and computer to the same local network, with communication between devices allowed. The computer must support network boot.
- USB: connect the phone to the computer with a data cable. The phone's kernel and USB configuration must support adding a mass storage function.

## PXE network boot

1. Install the APK, grant it root access, and import ISOs under Images.
2. Connect the phone and PC to the same LAN, open PXE, and tap **Start PXE**. The default ProxyDHCP mode lets the home router assign IP addresses.
3. Select **UEFI IPv4 network boot** on the PC, then choose an ISO. If the phone is still preparing an image, use **Refresh ISO list** once it finishes. Keep the phone connected and PXE running during installation.

The local ISO menu supports x64 UEFI computers and recognizes standard Windows Setup, WinPE, Ubuntu Casper, fnOS, and Debian installer layouts. Some WinPE images require a Windows x64 Setup ISO in the library. Preparing Windows Setup requires extra storage for the extracted installation files. The menu marks unsupported layouts. Online boot entries require internet access.

Open **Advanced settings** to specify a boot file, edit the iPXE script, or use full DHCP on an isolated network. Restore the default script to use the automatic ISO menu.

## Images and USB boot

1. Download or import an ISO on the Images page and wait until it is ready.
2. Connect the phone to the computer with a data cable, tap the image's USB icon, and enable USB boot. If preparation is required, progress is shown and you can cancel. The original image remains unchanged.
3. Select the phone's read-only disk or optical drive in the computer's boot menu.
4. When finished, stop USB boot in the app and wait for the phone's USB configuration to be restored before unplugging the cable. If restoration fails, retry; if it still fails, restart the phone.

Do not switch USB modes while USB boot is active. File transfer and USB debugging may temporarily disconnect. Compatibility depends on the image, computer firmware, and phone drivers.

### Optional: include Windows drivers

Use the image's **Include drivers** icon to select a ZIP or folder containing a complete INF driver package and add it to the Windows/WinPE USB media, without requiring the computer to be online.
Windows Setup can discover the drivers automatically, or you can browse the `$WinPEDriver$` folder through **Load driver**; standalone WinPE requires manual loading.

## Preview

<div style="display:inline-block">
<img src=".github/image/demo1.jpg" alt="App screenshot 1" width="230">
<img src=".github/image/demo2.jpg" alt="App screenshot 2" width="230">
<img src=".github/image/demo3.jpg" alt="App screenshot 3" width="230">
</div>

## Build and release

Use the same toolchain as CI: JDK 25, Android SDK 37, NDK 28.2.13676358, and Go 1.27.4.
The JVM target is 17, and the Gradle version is pinned by the project wrapper.

```bash
./gradlew testDebugUnitTest lintRelease
./gradlew assembleRelease -PnetbootVersionName=1.0.3
# Only when the emulator ABI is needed
./gradlew assembleDebug -PnetbootAbis=arm64-v8a,armeabi-v7a,x86_64
# Requires a connected, dedicated test device
./gradlew connectedDebugAndroidTest
```

See [AGENTS.md](AGENTS.md) for development guidelines and testing requirements, and [NOTICES.md](NOTICES.md) for third-party sources and licenses.
