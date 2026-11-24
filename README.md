# NetBoot-PXE

简体中文 | [English](README.en.md)

让已获 Root 权限的 Android 手机提供 **PXE 网络启动**或**只读 USB 启动盘**，
并支持微软官方 Windows ISO 下载、本地镜像导入和断点续传。

## 使用前提

> 临时root工具 [GhostLock-App](https://github.com/YuKongA/ghostlock-app/blob/main/README_ZH.md) ， [DirtyFrag](https://github.com/mitschud/DirtyFrag)

- Android 8.0 及以上，已在 Root 管理器中授权本应用使用 `su`。应用启动时自动检测现有权限，不安装或修补 Root。
- PXE：手机和电脑接入同一局域网，网络允许设备互访；电脑支持网络启动。
- USB：使用数据线连接电脑；手机内核和 USB 配置需支持添加存储功能。

## PXE 网络启动

1. 在手机安装 APK 并授予 Root 权限，然后在「镜像」页导入 ISO。[下载 APK](https://github.com/zxmls-ai/netboot-pxe/releases/latest)
2. 将手机和电脑接入同一局域网。在手机上点「PXE」→「启动 PXE」。家庭网络保留默认的 **ProxyDHCP**。
3. 电脑选择 **UEFI IPv4 网络启动**，从菜单中选择镜像。镜像准备完成后，可以在电脑上选 **Refresh ISO list** 更新菜单。安装期间保持手机联网和 PXE 运行。

本地镜像菜单面向 x64 UEFI，可识别标准 Windows 安装盘、WinPE、Ubuntu Casper、fnOS 和 Debian 安装盘布局。部分 WinPE 需要镜像库中另有一个 Windows x64 安装 ISO。首次准备 Windows 安装盘需要额外空间存放解包后的安装文件。

「高级设置」可配置自定义 iPXE 脚本、启动文件和隔离网络的完整 DHCP。使用默认脚本时会生成本地 ISO 菜单。

## 镜像与 USB 启动盘

1. 在「镜像」页下载或导入 ISO，等待状态变为「可用」。
2. 用数据线连接电脑，点击镜像的 USB 图标并选择「启用」。需要制作时会显示进度，可取消，原始镜像不变。
3. 在电脑启动菜单中选择手机提供的只读磁盘或光驱。
4. 用完先在应用中「停止 USB 启动」，待恢复后再拔线；恢复失败时先重试，仍失败则重启手机。

使用期间请勿切换 USB 模式，文件传输和 USB 调试可能暂时断开。实际兼容性取决于镜像、电脑固件和手机驱动。

### 可选：携带 Windows 驱动

点击镜像的「携带驱动」图标，选择包含完整 INF 驱动包的 ZIP 或文件夹，即可将驱动加入 Windows／WinPE USB 启动介质，无需电脑联网。
Windows 安装程序可自动发现，也可通过「加载驱动」浏览 `$WinPEDriver$` 文件夹；独立 WinPE 需手动加载。

## 预览

<div style="display:inline-block">
<img src=".github/image/demo1.jpg" alt="demo1" width="230">
<img src=".github/image/demo2.jpg" alt="demo2" width="230">
<img src=".github/image/demo3.jpg" alt="demo2" width="230">
</div>

---

开发规范见 [AGENTS.md](AGENTS.md)，第三方许可见 [NOTICES.md](NOTICES.md)。
