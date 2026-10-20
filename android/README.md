# GVN Explorer on an Android phone

Turn a retired Android phone into a home file server: the phone stays on
the charger, the share lives on its storage (or an SD card / USB stick),
and every laptop/phone in the house opens the normal web GUI over Wi-Fi.
No router with a USB port needed.

## What this app is

The APK is a thin wrapper — **the server itself is the ordinary static
Go binary** (the same one from the release archives, with the web GUI
baked in). The app runs it as a foreground service so Android doesn't
kill it, prompts for the two system settings that matter (all-files
access + battery exemption), and autostarts it after a reboot if you
tick that. Accounts/ACLs (`perms.db`) live in the app's own storage, so
moving the shared folder to a different volume never touches them.

## Install

1. Copy `gvn-explorer-…-android.apk` onto the phone and install it
   (Settings → allow "install unknown apps"). Built by
   `make android-apk` — see "Building" below.
2. Open the app once and press **"All-files access"** → allow
   (Android calls it *All files access*; on Android 10 or older there is
   no such prompt, the old storage permission is asked automatically).
3. Press **"Ignore battery limits"** → allow. On Xiaomi/Huawei/Oppo
   phones also lock the app in the recent-apps list ("autostart"),
   their killers are stricter than stock Android's.
4. Pick the **Storage location**: the dropdown lists the phone storage,
   and any SD card or USB stick you have plugged in (press *Rescan
   volumes* after plugging one in). The shared folder defaults to a
   `gvn` subfolder of the volume you pick; edit the path if you want
   another one.
5. Press **Start**. The status line shows the URL, e.g.
   `https://192.168.1.42:8443` — open that from any device on your
   Wi-Fi (one-time "self-signed certificate" warning, same as the
   desktop binary), and log in as `system / admin / admin`.

Change the port or path any time; press Stop, edit, Start. Tick
**Start after reboot** if the phone is a permanent server.

## What to share from

- **Internal storage** always works (`/storage/emulated/0/gvn`).
- **SD card**: works if the phone has a slot; it shows up as a second
  volume in the dropdown.
- **USB stick (OTG)**: works only if the phone supports USB host mode
  and powers the stick — a pass-through charging hub fixes power-hungry
  drives. NTFS sticks are usually read-only on Android; FAT/exfat/ext4
  are fine.
- The phone itself can't be *mounted* by other devices — everything
  goes through the web GUI/API, like any other gvn-explorer install.

## Honest caveats

- **Battery**: an old Li-Ion cell on 24/7 charge can swell. A trickle
  charger or a smart plug (off at ~80%) is worth more than any setting.
- **Capacity**: old phones have 8–16 GB and usually no expansion —
  fine for documents and some video, not a 4 TB media wall.
- **Wi-Fi**: the phone gets a new IP after reboots; reserve one for it
  in your router if the URL bothers you.
- Android kills network sockets when Doze gets aggressive and OEM
  killers get creative — the foreground-service + battery-exemption
  pair is the standard answer and is exactly what the two buttons set
  up. If the server disappears anyway, re-open the app; the log tail
  at the bottom shows why.
- **Performance** is fine: serving bytes and Range requests is light;
  video playback work happens on the device watching the video.

## Building

```sh
make android-apk        # → dist/gvn-explorer-<ver>-android.apk
```

That builds the server binary for `arm64-v8a`, `armeabi-v7a` and
`x86_64` into `android/app/src/main/jniLibs/` (named `libgvnserver.so`
so the installer extracts it into the app's native-library dir — the
only place a modern Android app may exec from), then packages the
Kotlin wrapper with the Gradle wrapper in `android/` (needs JDK 17 and
the Android SDK; `ANDROID_HOME` defaults to `~/Library/Android/sdk`,
override it if yours lives elsewhere). The wrapper app itself has no
third-party dependencies — plain framework widgets and ~500 lines of
Kotlin (`ServerService` exec's the binary and keeps it alive,
`MainActivity` is the single settings screen).
