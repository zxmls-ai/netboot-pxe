package com.sky22333.netboot.data

import android.content.Context
import android.os.ParcelFileDescriptor
import com.sky22333.netboot.core.mobilecore.Mobilecore
import com.sky22333.netboot.core.mobilecore.Preparation
import java.io.File
import java.io.IOException
import java.nio.file.Files
import java.nio.file.StandardCopyOption
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.isActive

internal class PxeCatalog(
    private val context: Context,
    private val address: String,
    private val password: String,
) {
    private val files = context.filesDir
    private val root = File(files, "pxe/iso-menu")
    private val items = File(root, "items")
    private val share = File(files, "pxe-source")
    private val staging = File(files, "pxe-catalog-staging")
    private val imageRoot = File(files, "iso").canonicalFile

    fun initialize(assets: List<IsoAssetEntity>) {
        for (directory in listOf(root, items, share, staging)) {
            check(directory.mkdirs() || directory.isDirectory) { "media_io_failed" }
        }
        val retained = assets.mapNotNull(::exportId).toSet()
        for (directory in listOf(items, share)) {
            directory.listFiles().orEmpty().filter { it.name !in retained }
                .forEach { it.deleteRecursively() }
        }
        for (name in BootAssets) {
            val destination = File(root, name)
            context.assets.open("pxe/catalog/$name").use { input ->
                destination.outputStream().buffered().use { input.copyTo(it) }
            }
        }
        val defaultMenu = context.assets.open("pxe/autoexec.ipxe").bufferedReader().use { it.readText() }
        publishText(File(root, "online.ipxe"), defaultMenu)
        publishText(File(root, "connect.cmd"), connectionScript())
        publishText(File(share, "netboot-source.txt"), "NetBoot Windows installation media\n")
        publish(assets.mapNotNull { asset ->
            exportId(asset)?.let { Entry(it, menuLabel(asset.fileName), cachedKind(it) ?: "pending") }
        }.distinctBy { it.id })
    }

    suspend fun update(assets: List<IsoAssetEntity>, failed: suspend (String, String) -> Unit) {
        val candidates = assets.mapNotNull { asset -> exportId(asset)?.let { it to asset } }.distinctBy { it.first }
        val entries = candidates.map { (id, asset) ->
            Entry(id, menuLabel(asset.fileName), cachedKind(id) ?: "pending")
        }.toMutableList()
        publish(entries)
        for ((index, candidate) in candidates.withIndex()) {
            if (entries[index].kind != "pending") continue
            val (id, asset) = candidate
            currentCoroutineContext().ensureActive()
            val kind = try {
                prepare(asset, id)
            } catch (error: Exception) {
                currentCoroutineContext().ensureActive()
                if (error is kotlinx.coroutines.CancellationException) throw error
                failed(asset.fileName, error.message.orEmpty())
                "failed"
            }
            entries[index] = entries[index].copy(kind = kind)
            publish(entries)
        }
    }

    private fun cachedKind(id: String): String? {
        val marker = File(items, "$id/kind")
        val kind = if (marker.isFile) marker.readText().trim() else return null
        if (kind == "windows" && !File(share, "$id/setup.exe").isFile) return null
        if (kind in LinuxKinds && !File(items, "$id/iso-file").isFile) return null
        return kind
    }

    private suspend fun prepare(asset: IsoAssetEntity, id: String): String {
        val boot = File(items, id)
        val install = File(share, id)
        val iso = File(asset.filePath).canonicalFile
        if (iso.parentFile != imageRoot || !iso.isFile || iso.length() != asset.fileSize) {
            throw IOException("media_invalid_iso")
        }
        val work = Files.createTempDirectory(staging.toPath(), "image-").toFile()
        val bootWork = File(work, "boot").apply { mkdir() }
        val installWork = File(work, "source").apply { mkdir() }
        val scope = currentCoroutineContext()
        try {
            val native = ParcelFileDescriptor.open(iso, ParcelFileDescriptor.MODE_READ_ONLY).use { input ->
                NativeMedia.prepareUdfPxe(input.fd, bootWork.path, installWork.path, object : MediaProgress {
                    override fun onProgress(stage: Int, done: Long, total: Long) = scope.isActive
                })
            }
            val kind = native ?: Mobilecore.prepareISO(iso.path, bootWork.path, installWork.path, object : Preparation {
                override fun cancelled() = !scope.isActive
            })
            scope.ensureActive()
            when (kind) {
                "casper", "fnos", "debian" -> File(bootWork, "iso-file").writeText(iso.name)
                "windows" -> {
                    val windowsFiles = File(installWork, "sources")
                    val bcd = File(installWork, "efi/microsoft/boot/bcd").takeIf(File::isFile)
                        ?: File(installWork, "boot/bcd")
                    val sdi = File(installWork, "boot/boot.sdi").takeIf(File::isFile)
                        ?: File(windowsFiles, "boot.sdi")
                    for ((name, source) in listOf(
                        "BCD" to bcd, "boot.sdi" to sdi,
                        "bootmgfw.efi" to File(installWork, "efi/boot/bootx64.efi"),
                        "boot.wim" to File(windowsFiles, "boot.wim"),
                    )) {
                        Files.copy(source.toPath(), File(bootWork, name).toPath())
                    }
                    install.deleteRecursively()
                    Files.move(installWork.toPath(), install.toPath(), StandardCopyOption.ATOMIC_MOVE)
                }
            }
            File(bootWork, "kind").writeText(kind)
            boot.deleteRecursively()
            Files.move(bootWork.toPath(), boot.toPath(), StandardCopyOption.ATOMIC_MOVE)
            return kind
        } finally {
            work.deleteRecursively()
        }
    }

    private fun publish(entries: List<Entry>) {
        val donor = entries.firstOrNull { it.kind == "windows" }?.id
        val statuses = entries.associate { entry ->
            entry.id to when (entry.kind) {
                "windows" -> "Windows Setup"
                "casper" -> "Ubuntu Casper"
                "fnos" -> "fnOS"
                "debian" -> "Debian installer"
                "winpe" -> if (canBootPE(entry.id, donor)) "WinPE" else "Import a Windows x64 setup ISO first"
                "pending" -> "Preparing"
                "failed" -> "Preparation failed"
                else -> "Unsupported ISO layout"
            }
        }
        for (entry in entries) {
            val text = when (entry.kind) {
                "windows" -> windowsScript(entry.id, donor, installing = true)
                "winpe" -> if (canBootPE(entry.id, donor)) windowsScript(entry.id, donor, installing = false) else null
                "casper", "fnos", "debian" -> linuxScript(entry)
                else -> null
            }
            if (text != null) publishText(File(items, "${entry.id}/go.ipxe"), text)
        }
        publishText(File(root, "menu.ipxe"), buildString {
            appendLine("#!ipxe")
            appendLine(":menu")
            appendLine("menu NetBoot ISO Library (x64 UEFI)")
            if (entries.isEmpty()) appendLine("item --gap Import an ISO in the NetBoot app.")
            for (entry in entries) appendLine("item i${entry.id} ${entry.label}  [${statuses.getValue(entry.id)}]")
            appendLine("item refresh Refresh ISO list")
            appendLine("item online Online boot menu")
            appendLine("item quit Exit")
            appendLine("choose selected || exit")
            appendLine("goto \${selected}")
            for (entry in entries) {
                appendLine(":i${entry.id}")
                if (entry.kind in LinuxKinds || entry.kind == "windows" || entry.kind == "winpe" && canBootPE(entry.id, donor)) {
                    appendLine("chain items/${entry.id}/go.ipxe || goto failed")
                } else {
                    appendLine("echo ${statuses.getValue(entry.id)}")
                    appendLine("prompt Press any key to return")
                }
                appendLine("goto menu")
            }
            appendLine(":refresh")
            appendLine("chain --replace menu.ipxe")
            appendLine(":online")
            appendLine("chain online.ipxe || goto failed")
            appendLine("goto menu")
            appendLine(":failed")
            appendLine("imgfree")
            appendLine("prompt Boot failed. Press any key to return")
            appendLine("goto menu")
            appendLine(":quit")
            appendLine("exit")
        })
    }

    private fun canBootPE(id: String, donor: String?): Boolean =
        donor != null || File(items, "$id/boot.sdi").isFile

    private fun windowsScript(id: String, donor: String?, installing: Boolean): String = buildString {
        appendLine("#!ipxe")
        appendLine("kernel ../../wimboot || exit 1")
        val bootFiles = if (installing || donor == null) "." else "../$donor"
        for (name in listOf("BCD", "boot.sdi")) {
            appendLine("initrd --name $name $bootFiles/$name $name || exit 1")
        }
        if (installing) {
            installLaunchFiles(id)
            for ((name, source) in listOf("winpeshl.ini" to "winpeshl.ini", "netsetup.cmd" to "netsetup.cmd", "connect.cmd" to "../../connect.cmd")) {
                appendLine("initrd --name $name $source $name || exit 1")
            }
        } else if (donor != null) {
            appendLine("initrd --name bootmgfw.efi $bootFiles/bootmgfw.efi bootmgfw.efi || exit 1")
        }
        appendLine("initrd --name boot.wim boot.wim boot.wim || exit 1")
        appendLine("boot")
    }

    private fun installLaunchFiles(id: String) {
        publishText(File(items, "$id/winpeshl.ini"), "[LaunchApps]\r\n%SYSTEMROOT%\\System32\\cmd.exe, /k %SYSTEMROOT%\\System32\\netsetup.cmd\r\n")
        val lines = listOf(
            "@echo off", "wpeinit", ":connect", "call %SystemRoot%\\System32\\connect.cmd",
            "if errorlevel 1 goto retry", "set media=Z:\\$id",
            "echo Installation source connected. Opening Windows Setup...",
            "if not exist \"%SystemDrive%\\sources\\setup.exe\" goto fromshare",
            "if not exist \"%media%\\sources\\install.wim\" goto fromshare",
            "start \"\" /wait \"%SystemDrive%\\sources\\setup.exe\" \"/InstallFrom:%media%\\sources\\install.wim\"",
            "exit /b %errorlevel%", ":fromshare", "start \"\" /wait \"%media%\\setup.exe\"",
            "exit /b %errorlevel%", ":retry", "echo Windows installation source is unavailable.", "pause", "goto connect",
        )
        publishText(File(items, "$id/netsetup.cmd"), lines.joinToString("\r\n", postfix = "\r\n"))
    }

    private fun connectionScript(): String = listOf(
        "@echo off", "setlocal", "wpeutil InitializeNetwork", "net start LanmanWorkstation >nul 2>&1",
        "set attempts=0", ":connect", "if exist Z:\\netboot-source.txt exit /b 0",
        "net use Z: \\\\$address\\install /user:NETBOOT\\nbread $password /persistent:no >\"%TEMP%\\nbsource.log\" 2>&1",
        "if not errorlevel 1 exit /b 0", "set /a attempts+=1 >nul",
        "if %attempts% GEQ 40 goto report", "ping -n 4 127.0.0.1 >nul", "goto connect",
        ":report", "type \"%TEMP%\\nbsource.log\"", "ipconfig", "exit /b 1",
    ).joinToString("\r\n", postfix = "\r\n")

    private fun linuxScript(entry: Entry): String {
        val boot = when (entry.kind) {
            "casper" -> "rdinit=/netboot/init boot=casper live-media=/dev/nbd0"
            "fnos" -> "rdinit=/netboot/init priority=high"
            else -> "priority=high"
        }
        val hook = if (entry.kind == "debian") {
            "nbd-debian /usr/lib/debian-installer-startup.d/S28netboot-disk"
        } else {
            "nbd-init /netboot/init"
        }
        return """
            #!ipxe
            kernel vmlinuz initrd=initrd.magic $boot nbserver=$address nbexport=${entry.id} nbmac=${D}{netX/mac} nbip=${D}{netX/ip} nbmask=${D}{netX/netmask} nbgateway=${D}{netX/gateway} --- || exit 1
            initrd initrd || exit 1
            initrd ../../nbd-busybox /netboot/busybox mkdir=1 mode=755 || exit 1
            initrd ../../nbd-mount /netboot/mount-disk mode=755 || exit 1
            initrd ../../nbd-dhcp /netboot/dhcp mode=755 || exit 1
            initrd ../../$hook mode=755 || exit 1
            boot
        """.trimIndent() + "\n"
    }

    private fun publishText(target: File, body: String) {
        val tmp = Files.createTempFile(staging.toPath(), "publish-", ".tmp").toFile()
        try {
            tmp.writeText(body)
            Files.move(tmp.toPath(), target.toPath(), StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING)
        } finally {
            tmp.delete()
        }
    }

    private fun exportId(asset: IsoAssetEntity): String? = asset.sha256
        .takeIf { asset.state == IsoState.Ready && Hash.matches(it) }?.take(32)

    private fun menuLabel(name: String): String = name.map { c ->
        if (c.isLetterOrDigit() || c in " ._()+-") c else '_'
    }.joinToString("").take(105)

    private data class Entry(val id: String, val label: String, val kind: String)

    private companion object {
        const val D = '$'
        val Hash = Regex("[0-9a-f]{64}")
        val LinuxKinds = setOf("casper", "fnos", "debian")
        val BootAssets = listOf("wimboot", "nbd-busybox", "nbd-mount", "nbd-init", "nbd-dhcp", "nbd-debian")
    }
}
