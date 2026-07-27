# Scoped service-control grant

The service account may run ONLY `systemctl start/stop/restart/status`
against ONLY the managed server unit — never blanket sudo (§5.2, §8).

Mechanism (sudoers rule vs polkit policy) is an open item (§11); both
templates will live here and `install.sh` ships the winner.
