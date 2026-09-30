# Paladin

A web admin panel for **one** Palworld dedicated server. It runs on the same
Linux box as the server, you open a browser, and you manage everything from
there. No Docker, no cloud, no accounts — just your server and a dashboard.

I built this because the existing tools all had the same two problems: their
settings lists went stale (new game options just... never showed up in the
UI), and when you changed a setting and nothing happened, they'd cheerfully
tell you it was "applied" anyway. Paladin's whole reason to exist is fixing
those two things properly. Everything else grew from there.

## What it actually does

- **Dashboard** — live FPS, players, uptime, host CPU/memory/network, and an
  activity feed of everything happening on the server. If you're on a VM it
  even shows steal time so you know when to blame your hypervisor instead of
  Palworld.
  <img width="2873" height="1622" alt="paladinrel1" src="https://github.com/user-attachments/assets/aa07a4a9-be44-4f4d-a01f-4b038cdef362" />

- **Server settings** — every setting the game actually supports, current as
  of 1.0, with tooltips explaining what things do and warnings on the traps
  (looking at you, level-gated base cap). Changes are staged, then applied as
  one clean cycle: warn players → save → stop → back up → apply → start →
  verify it actually took.
  <img width="2866" height="1623" alt="paladinrel4" src="https://github.com/user-attachments/assets/0065f32d-e393-4fb4-9deb-52368d86b5f6" />

- **Live world map** — your players and nearby wild Pals on the real map,
  with real in-game coordinates. Updates every few seconds.
  <img width="2863" height="1625" alt="paladinrel3" src="https://github.com/user-attachments/assets/963615f2-6a44-419e-9a71-e9b1afdec2d6" />

- **Players** — who's online now, and everyone who's ever played (pulled
  from the world save), with levels, guilds, bases, Pal counts, and last-seen
  times. Kick and ban from the roster.
<img width="2870" height="1626" alt="paladinrel2" src="https://github.com/user-attachments/assets/83df66be-2def-48c5-bf19-d38ef71a9e1c" />
<img width="2864" height="1613" alt="paladin7" src="https://github.com/user-attachments/assets/57f76521-3c5b-4b89-87e8-ab2d8aa85b03" />

- **Backups** — one click, plus automatic backups before anything risky.
  Restore works even when the server is down, which is exactly when you need
  it.
  <img width="2863" height="1620" alt="paladinrel5" src="https://github.com/user-attachments/assets/44012f65-3d65-45c0-bd09-edbfc8fca38a" />

- **Auto-restart** — the server leaks memory over time (it just does). Set a
  threshold and Paladin saves the world and restarts it before it falls over,
  with a warning to players first. Crash restarts are handled by systemd.
- **Updates** — when Pocketpair ships a server update, a badge lights up.
  One button: warn players, save, stop, back up, update, restart, verify.
<img width="2866" height="1622" alt="paladinrel6" src="https://github.com/user-attachments/assets/e9727509-985f-4fef-a711-711c22504b26" />

The history survives restarts, the log is a plain text file you can grep,
and the whole thing is a single static binary.

## Install

On the box you want to run it on (fresh or existing — see below):

```bash
curl -fsSL https://raw.githubusercontent.com/juicetheforce/palworld-paladin/main/scripts/install.sh | sudo bash
```

It detects what's already on the machine, tells you what it found, and asks
before changing anything. Three scenarios it handles:

1. **Fresh box** — installs SteamCMD, the Palworld server, and Paladin, all
   under a dedicated service account, all supervised by systemd. One command,
   go make coffee (the server download is ~5 GB), come back to a URL.
2. **Existing server, nothing managing it** — figures out which user runs
   it and where it lives (by reading the actual process, not guessing), and
   takes over supervision with your consent.
3. **Existing server with another manager** — same as above, but it'll ask
   before stopping and disabling whatever was managing it first.

Docker-based servers are politely declined — those are configured through
their compose files and Paladin won't fight the Docker daemon for them.

Run it with `--check` first if you want to see what it would find without it
touching anything. I'd recommend that on any box you care about.

**Updating Paladin** is the same command. It notices Paladin is installed,
compares versions, and swaps the binary. Your server isn't touched.

## First login: your setup token

The first time you open Paladin in a browser, it asks you to create a
password. Before it lets you, it also asks for a **setup token**: a one-time
code that proves you're the person who installed it. Without it, anyone else
on your network who happened to open the page first could create the admin
account before you did.

**Where to find it:** the installer prints it at the end, right under the
Web UI address:

```
[paladin]    Web UI:        http://192.168.1.50:8080
[paladin]    Setup token:   (a 32-character code of letters and numbers)
```

Copy it into the "Setup token" box, choose your password, and you're in.

**If you missed it:** run this on the server:

```bash
sudo paladin setup-token
```

If that says `command not found` (some distros don't let `sudo` see
`/usr/local/bin`), use the full path instead:

```bash
sudo /usr/local/bin/paladin setup-token
```

Rerunning the installer also prints it. The token stays valid until you
create your admin account, however long that takes.

**After setup:** the token is deleted, and you won't need it again. Running
`sudo paladin setup-token` afterwards just tells you setup is already
complete.

**Forgot your password?** There's no reset button or command yet. What works
today is starting the first-run setup over, which keeps your server, world,
settings and backups exactly as they are:

1. Find where Paladin keeps its data (on a fresh install it's
   `/home/palworld`):

   ```bash
   sudo grep data_dir /etc/paladin/config.json
   ```

2. Delete the login file inside it, then restart Paladin:

   ```bash
   sudo rm /home/palworld/paladin-config/auth.json   # use your data_dir from step 1
   sudo systemctl restart paladin
   ```

3. Get a fresh setup token with `sudo paladin setup-token`, open the web UI,
   and create a new password.

**Keep it off the internet.** Paladin listens on all of the server's network
addresses so your phone and other devices on your home network can reach it.
That also means you should keep its port (8080 by default) away from the
internet: don't port-forward it on your router, and if the server has a
public IP, block the port in its firewall.

## Requirements

- Linux, x86_64, systemd (built and tested on Ubuntu 24.04)
- 4+ cores, 16 GB RAM, ~64 GB disk — those are really the *game server's*
  requirements; Paladin itself is a rounding error next to it

## Uninstall

```bash
sudo ./scripts/uninstall.sh               # removes Paladin, leaves your server running
sudo ./scripts/uninstall.sh --everything  # also removes server supervision
```

Removing the admin panel never takes your game down with it. Deleting the
actual server files (and your world) is a separate step that makes you type
DELETE, because I've been burned before and so have you. Your backups and
Paladin's data are always left where they are — they're yours.

## Building from source

```bash
go build -o paladin ./cmd/paladin      # the web UI is embedded, no Node needed
./scripts/release.sh vX.Y.Z            # or build a proper release artifact
```

## Credits & licenses

Paladin is Apache-2.0 and stands on some excellent community work:

- **Save parsing** uses the `sav_cli` sidecar from
  [palworld-server-tool](https://github.com/zaigie/palworld-server-tool)
  (PST). The installer downloads a pinned, checksummed release of it at
  install time — it runs as a separate process and is never bundled into
  Paladin, because its Oodle-decompression dependencies are GPL-3.0 and
  Paladin isn't.
- **Map coordinate math** from
  [palworld-coord](https://github.com/palworldlol/palworld-coord) (MIT).
- **Map artwork** is © Pocketpair, stitched from PST's map tiles.
- The Palworld REST API belongs to Pocketpair; Paladin just uses it.

## Status

v0.2.x. One server, one host, Linux only. It's been run hard on a real
server through every feature — but it's young, so keep backups on (it does
that itself, conveniently). Issues and PRs welcome, especially for the
settings data file when game patches add new keys — that's a JSON edit, not
a code change, and it's exactly the kind of thing I built it that way for.
