# Testing on a VM

Unit tests and the laptop checks catch a lot, but installer, permission and
browser behaviour only show up on a real machine (see `learnings.md`). This
is how to put the current working tree on a disposable VM **through the real
installer**, the same way users run it.

`scripts/deploy-test.sh` runs on the laptop. It:
1. builds the binary exactly like `release.sh` (the shared
   `scripts/lib/build.sh`), stamped `dev-<commit>`, plus `-dirty` if the
   tree has uncommitted changes;
2. copies the tarball, its `.sha256` and `scripts/install.sh` to the VM's
   `/tmp`;
3. runs `sudo bash /tmp/install.sh --local-archive /tmp/<tarball>` there,
   interactively.

Everything else in the installer runs for real: detection, SteamCMD, the
server download, the pinned `sav_cli` fetch and its checksums, units,
sudoers.

It only deploys to hosts listed in `scripts/test-hosts`, which is
gitignored. **Never list the live box there.**

## One-time prep

You need a Proxmox VM for each distro you test, plus two snapshots of the
Ubuntu one. The commands below use VM id `9001` and the ssh alias
`paladin-vm-ubuntu`; substitute your own.

### 1. SSH key on the laptop

Skip this if you already have `~/.ssh/id_ed25519`.

```bash
ssh-keygen -t ed25519 -C "paladin-test"
```

### 2. A clean Ubuntu VM

- Create an Ubuntu Server 24.04 VM: x86_64, 4+ cores, 16 GB RAM, ~64 GB
  disk. The game server needs that much; Paladin alone needs far less.
- Install `openssh-server` and create a normal user with sudo.
- The VM needs internet access: each fresh install downloads the ~5 GB
  server, SteamCMD and `sav_cli`.

Then, from the laptop:

```bash
ssh-copy-id you@<vm-ip>
```

Add an alias to `~/.ssh/config` on the laptop:

```
Host paladin-vm-ubuntu
    HostName <vm-ip>
    User you
```

Check it works, and bring the VM up to date:

```bash
ssh paladin-vm-ubuntu 'sudo apt update && sudo apt -y full-upgrade && sudo poweroff'
```

### 3. Snapshot it as "clean"

On the Proxmox host:

```bash
qm snapshot 9001 clean --description "Ubuntu 24.04, updated, ssh key, no Palworld"
qm start 9001
```

### 4. List it as a test host

On the laptop:

```bash
cp scripts/test-hosts.example scripts/test-hosts
# edit scripts/test-hosts: one ssh destination per line, e.g. paladin-vm-ubuntu
```

### 5. Snapshot an "installed" state

This is the starting point for update tests.

```bash
scripts/deploy-test.sh paladin-vm-ubuntu --fresh
```

Open the Web UI, enter the setup token the installer printed, and create a
password. Then, on the Proxmox host:

```bash
qm snapshot 9001 installed --description "fresh install via deploy-test + admin created"
```

### 6. Optional: Fedora (and RHEL-family)

- Repeat steps 2–4 with Fedora Server 44 (alias `paladin-vm-fedora`). Use
  `sudo dnf -y upgrade` instead of apt.
- Fresh installs support dnf.
- Fedora 44's sudo can see `/usr/local/bin`. RHEL/Alma/Rocky's can't: their
  default `secure_path` leaves it out. An Alma or Rocky VM is the way to see
  `sudo paladin setup-token` fail and check the full-path fallback.

## Running tests

**Fresh install:**
```bash
# Proxmox host:
qm rollback 9001 clean && qm start 9001
# laptop:
scripts/deploy-test.sh paladin-vm-ubuntu --fresh
```

**Update of an existing install:**
```bash
# Proxmox host:
qm rollback 9001 installed && qm start 9001
# laptop:
scripts/deploy-test.sh paladin-vm-ubuntu --update
```

About the run:
- `--fresh` and `--update` are safety assertions. The script refuses if the
  VM isn't in the state you said.
- With neither flag, the installer decides, as it does for users.
- `sudo` on the VM asks for your password, and the installer asks its
  normal questions in your terminal.
- At the end the script prints the host, the build stamp and the Web UI URL.
- The footer should show the `dev-…` stamp and no "· update" link.

Laptop-only check of the `--local-archive` path (no VM, no root):

```bash
scripts/test-install-archive.sh
```

## Checklist: first-run setup token

Run these on a fresh deploy (`--fresh` from `clean`) unless noted. The
service user on a fresh install is `palworld`.

- [ ] **The installer prints the token** next to the Web UI URL in the final
      summary.
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
- [ ] **Installer summary:**
  - The token sits in a highlighted SETUP TOKEN block directly under the
    Web UI line.
  - The REST line says "not your Paladin login".
  - The closing line says "enter the setup token shown above".
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
- [ ] **Older binary:** main's installer with the v0.3.0 release (the
      curl one-liner before v0.3.1 is published). The summary must NOT say
      "shown above"; it says the build predates setup tokens.
- [ ] **Missed-token recovery via a rerun:** on a fresh deploy *before*
      creating the admin, run `scripts/deploy-test.sh paladin-vm-ubuntu --update`.
  - Answer **N** at "Update Paladin …?": it prints "Aborted." then the
    token.
  - Run it again and answer **y**: it updates, then prints the token.
- [ ] **Existing admin sees no change:** `--update` from the `installed`
      snapshot. No token is printed; the normal login screen shows.
- [ ] **sudo on Fedora:** on the Fedora VM, `sudo paladin setup-token` works.
      Optional, on an Alma/Rocky VM: it says `command not found`, and
      `sudo /usr/local/bin/paladin setup-token` works.
