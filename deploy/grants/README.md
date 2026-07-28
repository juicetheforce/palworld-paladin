# Scoped service-control grant

Model A (DESIGN.md §5.2): Paladin runs as the unprivileged `palworld`
service account and does all file work natively as that user. The ONE
privileged operation — controlling the server's systemd unit — is granted
via a tightly-scoped rule that permits only specific `systemctl` verbs
against only the server's own unit, never blanket sudo (§8).

## sudoers (current, simplest)

`palworld-paladin.sudoers` — enumerated command allow-list. Install:

```
sudo install -m 0440 palworld-paladin.sudoers /etc/sudoers.d/palworld-paladin
sudo visudo -cf /etc/sudoers.d/palworld-paladin   # validate
```

Paladin invokes these via `sudo -n systemctl ...` (supervise.SudoRunner).

## polkit (alternative, §11 open item)

A polkit policy is the alternative mechanism (finer-grained, no PATH
concerns, but more moving parts). Deferred; sudoers is the pragmatic
default for v1. If polkit wins later, it drops in behind the same
supervise.Runner seam with no engine changes.
