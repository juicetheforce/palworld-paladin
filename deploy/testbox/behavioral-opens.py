#!/usr/bin/env python3
"""behavioral-opens.py — resolve the runtime [open] questions from
docs/DESIGN.md §11 against the live test server. Run on the test box:

    sudo python3 behavioral-opens.py

Experiments (least -> most disruptive):
  E1  /game-data cost baseline (timing, payload, actor census) + /metrics
  E2  offline ban: does POST /ban accept a never-connected ID, where does
      banlist.txt live, what format, does /unban reverse it
  E3  hot backup: REST /save then live world-folder copy; is Level.sav
      stable across the copy window (autosave is the antagonist)
  E4  the "server rewrites its ini on shutdown" claim: marker edit, stop,
      inspect, restore, restart, verify REST readiness

E4 stops and restarts the game server. Fine on the test box; don't run
against a server people play on. Writes behavioral-report.json beside
itself. Part of palworld-paladin (deploy/testbox/).
"""
import base64, hashlib, json, re, shutil, subprocess, sys, time, urllib.request
from pathlib import Path

BASE      = "http://127.0.0.1:8212/v1/api"
CREDS     = Path("/home/palworld/palserver-credentials.txt")
SERVERDIR = Path("/home/palworld/palserver")
INI       = SERVERDIR / "Pal/Saved/Config/LinuxServer/PalWorldSettings.ini"
SAVEROOT  = SERVERDIR / "Pal/Saved/SaveGames"
UNIT      = "palserver.service"
FAKE_ID   = "steam_00000000000000001"   # synthetic, never-connected
HERE      = Path(__file__).resolve().parent
REPORT    = {}

def die(m): sys.exit(f"ERROR: {m}")

def admin_pw():
    if not CREDS.exists(): die(f"{CREDS} not found (run with sudo?)")
    m = re.search(r"AdminPassword:\s*(\S+)", CREDS.read_text())
    if not m: die("could not parse AdminPassword from credentials file")
    return m.group(1)

PW = None
def api(method, path, body=None, timeout=20):
    req = urllib.request.Request(BASE + path, method=method)
    req.add_header("Authorization",
                   "Basic " + base64.b64encode(f"admin:{PW}".encode()).decode())
    data = None
    if body is not None:
        data = json.dumps(body).encode()
        req.add_header("Content-Type", "application/json")
    t0 = time.monotonic()
    try:
        with urllib.request.urlopen(req, data=data, timeout=timeout) as r:
            raw = r.read()
            return r.status, raw, time.monotonic() - t0
    except urllib.error.HTTPError as e:
        return e.code, e.read(), time.monotonic() - t0

def sha(p: Path):
    h = hashlib.sha256()
    with open(p, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()

def hdr(t): print("\n" + "=" * 74 + f"\n {t}\n" + "=" * 74)

def wait_rest(timeout=180):
    t0 = time.monotonic()
    while time.monotonic() - t0 < timeout:
        try:
            s, _, _ = api("GET", "/info", timeout=5)
            if s == 200: return True
        except Exception:
            pass
        time.sleep(2)
    return False

# ---------------------------------------------------------------- E1
def e1():
    hdr("E1: /game-data cost baseline (empty world = floor, not ceiling)")
    runs = []
    census = {}
    for i in range(5):
        s, raw, dt = api("GET", "/game-data", timeout=30)
        if s != 200:
            print(f"  poll {i+1}: HTTP {s} — endpoint problem, aborting E1")
            REPORT["e1"] = {"error": f"HTTP {s}"}
            return
        runs.append({"ms": round(dt * 1000, 1), "bytes": len(raw)})
        if i == 0:
            try:
                d = json.loads(raw)
                for a in d.get("ActorData", []) or []:
                    u = a.get("UnitType", a.get("Type", "?"))
                    census[u] = census.get(u, 0) + 1
            except Exception as ex:
                census = {"parse_error": str(ex)}
        time.sleep(1)
    for i, r in enumerate(runs):
        print(f"  poll {i+1}: {r['ms']} ms, {r['bytes']} bytes")
    print(f"  actor census (poll 1): {census}")
    s, raw, dt = api("GET", "/metrics")
    met = json.loads(raw) if s == 200 else {"error": s}
    print(f"  /metrics ({round(dt*1000,1)} ms): {met}")
    REPORT["e1"] = {"polls": runs, "actor_census": census, "metrics": met}

# ---------------------------------------------------------------- E2
def e2():
    hdr(f"E2: offline ban with synthetic id {FAKE_ID}")
    before = sorted(str(p) for p in SAVEROOT.rglob("banlist.txt"))
    print(f"  banlist.txt files before: {before or 'none'}")
    s, raw, _ = api("POST", "/ban", {"userid": FAKE_ID, "message": "paladin-test"})
    print(f"  POST /ban -> HTTP {s} {raw[:120]!r}")
    time.sleep(2)
    after = sorted(str(p) for p in SAVEROOT.rglob("banlist.txt"))
    contents = {}
    for p in after:
        contents[p] = Path(p).read_text(errors="replace")
        print(f"  {p} contents after ban:\n    {contents[p]!r}")
    if not after:
        print("  no banlist.txt found anywhere under SaveGames after ban!")
    s2, raw2, _ = api("POST", "/unban", {"userid": FAKE_ID})
    print(f"  POST /unban -> HTTP {s2} {raw2[:120]!r}")
    time.sleep(2)
    post_unban = {p: Path(p).read_text(errors="replace")
                  for p in (str(q) for q in SAVEROOT.rglob("banlist.txt"))}
    for p, t in post_unban.items():
        print(f"  {p} after unban:\n    {t!r}")
    REPORT["e2"] = {"ban_status": s, "ban_body": raw.decode(errors="replace"),
                    "banlist_paths": after, "banlist_after_ban": contents,
                    "unban_status": s2, "banlist_after_unban": post_unban,
                    "offline_ban_accepted": s == 200}

# ---------------------------------------------------------------- E3
def e3():
    hdr("E3: hot backup — /save then live copy; Level.sav stability")
    worlds = [d for d in (SAVEROOT / "0").iterdir() if d.is_dir()] \
             if (SAVEROOT / "0").exists() else []
    if not worlds:
        print("  no world folder under SaveGames/0 — has the world generated?")
        REPORT["e3"] = {"error": "no world folder"}
        return
    world = worlds[0]
    lvl = world / "Level.sav"
    print(f"  world folder: {world}")
    s, raw, dt = api("POST", "/save")
    print(f"  POST /save -> HTTP {s} in {round(dt*1000,1)} ms")
    time.sleep(2)
    h_before = sha(lvl) if lvl.exists() else None
    t0 = time.monotonic()
    dest = Path("/tmp/paladin-hotcopy")
    if dest.exists(): shutil.rmtree(dest)
    shutil.copytree(world, dest)
    copy_s = round(time.monotonic() - t0, 2)
    h_after = sha(lvl) if lvl.exists() else None
    h_copy  = sha(dest / "Level.sav") if (dest / "Level.sav").exists() else None
    size_mb = round(sum(f.stat().st_size for f in dest.rglob("*") if f.is_file()) / 1e6, 2)
    stable = h_before == h_after
    intact = h_copy in (h_before, h_after)
    print(f"  copy took {copy_s}s, {size_mb} MB")
    print(f"  Level.sav sha256 before copy == after copy: {stable}")
    print(f"  copied Level.sav matches a source snapshot: {intact}")
    print(f"  (AutoSaveSpan is 30s — a mid-copy autosave is the failure mode "
          f"this detects; a tiny fresh world copies fast, so also note the "
          f"copy duration vs 30s)")
    shutil.rmtree(dest)
    REPORT["e3"] = {"save_status": s, "copy_seconds": copy_s, "world_mb": size_mb,
                    "level_sav_stable_across_copy": stable,
                    "copied_file_matches_source": intact}

# ---------------------------------------------------------------- E4
def e4():
    hdr("E4: does the server rewrite PalWorldSettings.ini on shutdown?")
    original = INI.read_bytes()
    h_orig = hashlib.sha256(original).hexdigest()
    marker = f"paladin-marker-{int(time.time())}"
    text = original.decode()
    if 'ServerDescription=""' not in text:
        print("  ServerDescription not empty/found — using hash-only test")
        edited = False
    else:
        INI.write_text(text.replace('ServerDescription=""',
                                    f'ServerDescription="{marker}"', 1))
        edited = True
        print(f"  planted marker in live ini: {marker}")
    h_edit = sha(INI)
    print("  stopping server (graceful)...")
    subprocess.run(["systemctl", "stop", UNIT], check=True)
    for _ in range(30):
        r = subprocess.run(["pgrep", "-u", "palworld", "-f", "PalServer-Linux"],
                           capture_output=True)
        if r.returncode != 0: break
        time.sleep(1)
    h_stop = sha(INI)
    survived = marker in INI.read_text() if edited else None
    rewrote = h_stop != h_edit
    print(f"  ini hash changed across shutdown: {rewrote}")
    if edited:
        print(f"  marker survived shutdown: {survived}")
    verdict = ("REWRITES on shutdown — live edits are clobbered (Paladin's "
               "APPLY-after-STOP ordering is required, not optional)"
               if rewrote or survived is False else
               "does NOT rewrite on shutdown — claim not reproduced on 1.0.1 "
               "(APPLY-after-STOP remains correct ordering regardless)")
    print(f"  VERDICT: {verdict}")
    INI.write_bytes(original)
    print(f"  original ini restored (sha matches original: {sha(INI) == h_orig})")
    print("  starting server and waiting for REST readiness...")
    subprocess.run(["systemctl", "start", UNIT], check=True)
    ok = wait_rest()
    print(f"  server back up with REST answering: {ok}")
    REPORT["e4"] = {"marker_planted": edited, "hash_changed_on_stop": rewrote,
                    "marker_survived": survived, "verdict": verdict,
                    "server_restarted_ok": ok}
    if not ok:
        print("  !! server did not come back — check: journalctl -u palserver -e")

def main():
    global PW
    if subprocess.run(["id", "-u"], capture_output=True,
                      text=True).stdout.strip() != "0":
        die("run with sudo (file access + systemctl needed)")
    PW = admin_pw()
    s, raw, _ = api("GET", "/info")
    if s != 200: die(f"REST not answering (HTTP {s}) — is the server up?")
    print(f"Server: {json.loads(raw)}")
    e1(); e2(); e3(); e4()
    out = HERE / "behavioral-report.json"
    out.write_text(json.dumps(REPORT, indent=2))
    print(f"\nMachine-readable report: {out}")
    print("Paste it (or this output) back into the Claude chat.")

if __name__ == "__main__":
    main()
