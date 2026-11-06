# soap

Soap for your Mac.

soap is a terminal app for macOS. It finds junk, developer caches, large files, duplicate files and
unused apps, and it frees that space. It also shows what runs on your Mac: listening ports, network
connections and memory use.

Every clean-up shows a preview first. Most items go to the Trash, and you can put them back.

## Screens

Open soap and press a number key to switch screens.

| Key | Screen      | What it shows |
|-----|-------------|---------------|
| `0` | Home        | A dashboard: disk, memory, CPU, network, the things that need attention, and past clean-ups |
| `1` | Ports       | Listening ports, with the process or Docker container behind each port |
| `2` | Connections | Network connections, grouped by app, with the owner of each IP address (after `soap update-db`) |
| `3` | Memory      | Memory pressure and the apps that use the most memory |
| `4` | Junk        | App caches, browser caches, logs and crash reports, the Trash, iOS device backups |
| `5` | Dev         | Xcode data, simulators, Docker images and build cache, package caches, build folders in old projects |
| `6` | Disk        | A browser of your home folder, largest items first |
| `7` | Dupes       | Sets of identical files in your own folders, with the copy to keep |
| `8` | Apps        | Your apps, unused apps first, with their leftover files, and the leftovers of apps you removed |

Dev knows these package caches: npm, Yarn, pnpm, Composer, Gradle, CocoaPods, pip, Go and Homebrew.
In your projects it finds `node_modules`, `vendor`, `.venv`, `Pods`, `target`, `.next`, `.nuxt`,
`.turbo`, `.svelte-kit` and Gradle `build` folders.

## Install

You need macOS 13 Ventura or later. The app runs on Apple silicon and on Intel Macs.

1. Download `soap-macos-universal.tar.gz` from the latest release:
   <https://github.com/KhomerikiK/soap/releases/latest>

2. Open Terminal and go to your Downloads folder:

   ```bash
   cd ~/Downloads
   ```

3. Unpack the file:

   ```bash
   tar -xzf soap-macos-universal.tar.gz
   ```

4. Remove the download mark from the app:

   ```bash
   xattr -d com.apple.quarantine soap
   ```

   soap isn't notarized by Apple, so macOS blocks a downloaded copy and says it can't verify it.
   This command removes the "downloaded from the internet" mark from this one file only. If it
   prints `No such xattr`, the file has no mark, and you can go on.

5. Move soap to a folder on your PATH (macOS asks for your password):

   ```bash
   sudo mkdir -p /usr/local/bin
   ```

   ```bash
   sudo mv soap /usr/local/bin/
   ```

6. Start it:

   ```bash
   soap
   ```

To check the download first, also download `soap-macos-universal.tar.gz.sha256` from the same
release, and run this in `~/Downloads` before step 3:

```bash
shasum -a 256 -c soap-macos-universal.tar.gz.sha256
```

## Use

`soap` with no arguments opens the terminal UI. Press `?` to see all keys.

| Key | Action |
|-----|--------|
| `0`–`8` | Switch screen |
| `j` `k` or `↓` `↑` | Move |
| `enter` | Open the selected line (Home), open a folder (Disk) |
| `space` | Tick or untick an item |
| `a` | Tick the safe items only (Junk, Dev) |
| `c` | Clean what's ticked, after a preview |
| `h` | Show past clean-ups; `u` puts a trashed item back |
| `x` / `X` | Stop the selected process; `X` forces it |
| `/` | Filter rows |
| `r` | Scan again |
| `q` | Quit |

Each subcommand prints the same data as a screen, for scripts. Most of them take `--json`.

```text
soap ports              listening ports
soap conns              connections, grouped by app (--loopback includes this Mac)
soap mem                memory use and the top apps
soap junk               junk soap can clean
soap dev                developer junk soap can clean
soap disk [path]        the largest items in a folder (your home folder by default)
soap dupes              sets of identical files
soap apps               apps, unused first, with their leftovers
soap clean --safe       clean what the UI ticks by default
soap clean <id>...      clean items by their IDs (from junk, dev, disk, dupes or apps)
soap clean --dry-run    show what would be cleaned, and stop
soap history            past clean-ups
soap restore <id>       put an item from the Trash back where it was
soap kill :<port>       stop the process on a port (or: soap kill <pid>)
soap update-db          download the IP owner database (about 11 MB, from iptoasn.com)
```

Exit codes: `0` success, `1` error or cancelled, `2` nothing matched.

## How soap cleans

- **Nothing goes without a preview.** In the UI you tick items, press `c`, read the preview and
  press `y`. `soap clean` asks before it starts. Use `--dry-run` to only look.
- **Each item says how it goes.**
  - **To the Trash:** Disk, Dupes, Apps, Xcode archives and iOS device backups. You can put them
    back: press `h` and then `u`, or run `soap restore <id>`.
  - **Removed for good:** caches and logs that rebuild by themselves, project build folders that
    you reinstall (the item says how, such as `composer install`), and emptying the Trash.
  - **The tool's own command:** Docker images and build cache, old simulators and runtimes, and the
    Go, pnpm and Homebrew caches run that tool's own clean-up, such as `docker builder prune -a -f`
    or `brew cleanup --prune=all`.
- **Only safe items are ticked by default.** You tick everything else yourself.
- **soap never** asks for an admin password, runs clean-ups with `sudo`, removes Apple's own apps,
  or removes an app that is running.
- soap keeps a history of every clean-up in `~/Library/Application Support/soap/history.jsonl`.

## Permissions

- **Full Disk Access is optional.** Without it, soap skips the folders macOS protects and tells
  you what it skipped. To include them, open System Settings › Privacy & Security › Full Disk
  Access, and turn it on for your terminal app.
- **Quitting an app** from soap uses Apple Events. The first time, macOS may ask you to let your
  terminal control that app.

## Settings

soap writes its settings to `~/Library/Application Support/soap/config.json` the first time it runs:

```json
{
  "project_roots": ["~/Projects"],
  "stale_days": 30,
  "dupes_min_mb": 1,
  "unused_days": 30
}
```

| Setting | Meaning |
|---------|---------|
| `project_roots` | The folders where Dev looks for your projects |
| `stale_days` | Dev offers a project's build folders when the project didn't change for this many days |
| `dupes_min_mb` | Dupes leaves out files smaller than this many MB |
| `unused_days` | Apps calls an app unused when you didn't open it for this many days |

## Build from source

You need Go 1.27.3 or later, and the Xcode Command Line Tools, because soap calls macOS APIs
through cgo:

```bash
xcode-select --install
```

```bash
git clone https://github.com/KhomerikiK/soap.git
cd soap
go build -o soap ./cmd/soap
./soap
```

Run the tests with `go test ./...`. The tests never touch your home folder, your apps or your Trash.

To make the universal release build (Apple silicon and Intel, macOS 13 or later):

```bash
export MACOSX_DEPLOYMENT_TARGET=13.0 CGO_ENABLED=1 CGO_CFLAGS="-mmacosx-version-min=13.0 -O2" CGO_LDFLAGS="-mmacosx-version-min=13.0"
mkdir -p dist
GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o dist/soap-arm64 ./cmd/soap
GOARCH=amd64 CC="clang -arch x86_64" go build -trimpath -ldflags="-s -w" -o dist/soap-amd64 ./cmd/soap
lipo -create -output dist/soap dist/soap-arm64 dist/soap-amd64
codesign -s - -f dist/soap
```

## Uninstall

```bash
sudo rm /usr/local/bin/soap
```

To also remove the settings, the history and the IP database:

```bash
rm -rf ~/Library/Application\ Support/soap ~/Library/Caches/soap
```
