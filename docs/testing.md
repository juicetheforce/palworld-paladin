# Testing on a VM

Unit tests and the laptop checks catch a lot, but installer, permission and
browser behaviour only show up on a real machine (see `learnings.md`).

**The normal path is the simple one:**
1. Publish the release (`/release vX.Y.Z`, run by Claude Code when asked).
2. On a fresh VM, run the public curl one-liner, exactly as a user would.

That tests the real artifact from the real release, through the real
installer. `scripts/deploy-test.sh` (below) is an optional extra for trying
unreleased changes on a VM before publishing.

## One-time prep

You need a Proxmox VM for each distro you test, plus two snapshots of the
Ubuntu one. The commands below use VM id `9001`; substitute your own.

### 1. A clean Ubuntu VM

- Create an Ubuntu Server 24.04 VM: x86_64, 4+ cores, 16 GB RAM, ~64 GB
  disk. The game server needs that much; Paladin alone needs far less.
- Create a normal user with sudo, and install `openssh-server` if you want
  to work on it over ssh.
- The VM needs internet access: each fresh install downloads the ~5 GB
  server, SteamCMD and `sav_cli`.
- Bring it up to date and shut it down:

  ```bash
  sudo apt update && sudo apt -y full-upgrade && sudo poweroff
  ```

### 2. Snapshot it as "clean"

On the Proxmox host:

```bash
qm snapshot 9001 clean --description "Ubuntu 24.04, updated, no Palworld"
qm start 9001
```

### 3. Snapshot an "installed" state

This is the starting point for update tests. On the VM:

```bash
curl -fsSL https://raw.githubusercontent.com/juicetheforce/palworld-paladin/main/scripts/install.sh | sudo bash
```

Open the Web UI, enter the setup token the installer printed, and create a
password. Then, on the Proxmox host:

```bash
qm snapshot 9001 installed --description "fresh install via curl one-liner + admin created"
```

Retake this snapshot after each release if you want update tests to start
from the previous version.

### 4. Optional: Fedora (and RHEL-family)

- Repeat steps 1–2 with Fedora Server 44, using `sudo dnf -y upgrade`
  instead of apt. Fresh installs support dnf.
- Fedora 44's sudo can see `/usr/local/bin`. RHEL/Alma/Rocky's can't: their
  default `secure_path` leaves it out. An Alma or Rocky VM is the way to see
  `sudo paladin setup-token` fail and check the full-path fallback.

## Running tests (after a release is published)

**Fresh install.** On the Proxmox host:

```bash
qm rollback 9001 clean && qm start 9001
```

Then on the VM:

```bash
curl -fsSL https://raw.githubusercontent.com/juicetheforce/palworld-paladin/main/scripts/install.sh | sudo bash
paladin version            # must print the version you just released
```

**Update of an existing install.** On the Proxmox host:

```bash
qm rollback 9001 installed && qm start 9001
```

Then on the VM, run the same curl one-liner. It detects Paladin and updates
only the binary. Answer `y` when it asks.

## Optional: unreleased builds with deploy-test.sh

To try changes on a VM *before* publishing, `scripts/deploy-test.sh` runs on
the laptop. It:
1. builds the binary exactly like `release.sh` (the shared
   `scripts/lib/build.sh`), stamped `dev-<commit>`, plus `-dirty` if the
   tree has uncommitted changes;
2. copies the tarball, its `.sha256` and `scripts/install.sh` to the VM's
   `/tmp`;
3. runs `sudo bash /tmp/install.sh --local-archive /tmp/<tarball>` there,
   interactively.

Extra prep it needs:
- an ssh key on the laptop (`ssh-keygen -t ed25519`, then
  `ssh-copy-id you@<vm-ip>`);
- an `~/.ssh/config` alias (e.g. `paladin-vm-ubuntu`);
- the VM listed in `scripts/test-hosts` (gitignored; copy
  `scripts/test-hosts.example`).

It refuses any host that isn't listed there. **Never list the live box.**

```bash
scripts/deploy-test.sh paladin-vm-ubuntu --fresh    # from the clean snapshot
scripts/deploy-test.sh paladin-vm-ubuntu --update   # from the installed snapshot
```

About the run:
- `--fresh` and `--update` are safety assertions: the script refuses if the
  VM isn't in that state. With neither, the installer decides.
- `sudo` on the VM asks for your password, and the installer asks its usual
  questions.
- The footer shows the `dev-…` stamp and no "· update" link.

Laptop-only checks, with no VM and no root:

```bash
scripts/test-install-archive.sh      # install.sh --local-archive
scripts/test-installer-messages.sh   # the installer's first-login summary
```

## Checklist: first login and passwords

Run these on a fresh install from the `clean` snapshot unless noted. The
service user on a fresh install is `palworld`.

- [ ] **Installer summary:**
  - the token sits in a highlighted SETUP TOKEN block directly under the
    Web UI line;
  - the REST line says "not your Paladin login";
  - the closing line says "enter the setup token shown above".
- [ ] **The token file is private:**
      `sudo ls -l /home/palworld/paladin-config/setup-token` shows
      `-rw-------` owned by `palworld palworld`.
- [ ] **The token is never logged:**
      `sudo journalctl -u paladin --no-pager | grep -c "$(sudo cat /home/palworld/paladin-config/setup-token)"`
      prints `0`.
- [ ] **Root-created token is chowned:**
      ```bash
      sudo rm /home/palworld/paladin-config/setup-token
      sudo paladin setup-token
      sudo ls -l /home/palworld/paladin-config/setup-token   # owner palworld, not root
      ```
      Then use that token in the browser (proves Paladin can read it).
- [ ] **Step 1 in a browser:**
  - "step 1 of 2: enter your setup token" shows, with the
    `sudo paladin setup-token` hint;
  - with the token blank, Continue stays disabled;
  - a wrong token shows "That setup token is wrong…".
  - Check it at desktop width, ~380 px and ~340 px (browser dev tools), and
    on the Pixel Fold and the Titan 2.
- [ ] **Step 2:**
  - "Create your Paladin admin password" appears with password and confirm
    fields;
  - mismatched passwords give "The passwords don't match.";
  - matching passwords land on the dashboard.
  - Afterwards, `/home/palworld/paladin-config/setup-token` is gone, and
    `sudo paladin setup-token` says "Setup is already complete".
- [ ] **Missed-token recovery via a rerun:** *before* creating the admin,
      run the curl one-liner again. It says "Already up to date", then
      prints the token.
- [ ] **Change password** (menu → Change password):
  - a wrong current password is refused;
  - mismatched new passwords are refused;
  - a valid change works.
  - Then a second browser that was signed in is signed out, and the old
    password no longer works.
- [ ] **Forgot password:** `sudo paladin reset-password`.
  - Answer N: nothing changes.
  - Answer y: it prints a new token, and the Web UI goes back to step 1
    (any open tab gets signed out).
  - Complete both steps with a new password.
- [ ] **Existing admin sees no change:** from the `installed` snapshot, run
      the curl one-liner. No token is printed; the closing line says to sign
      in; the normal login screen shows.
- [ ] **sudo on Fedora:** on the Fedora VM, `sudo paladin setup-token`
      works. Optional, on an Alma/Rocky VM: it says `command not found`, and
      `sudo /usr/local/bin/paladin setup-token` works.

Installing an *older* binary with the current installer (the 2026-10-04
"shown above" case) can't be reproduced with the curl one-liner once a newer
release exists. `scripts/test-installer-messages.sh` covers it on the laptop.
