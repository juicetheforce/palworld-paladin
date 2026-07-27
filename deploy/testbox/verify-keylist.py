#!/usr/bin/env python3
"""verify-keylist.py — diff the REAL DefaultPalWorldSettings.ini against
Paladin's data/palworld-settings.json.

Run on the test box (needs read access to the palworld user's files):

    sudo python3 verify-keylist.py

Outputs a human-readable report and writes verify-report.json next to it.
Part of the palworld-paladin project (deploy/testbox/).
"""
import argparse, json, re, sys
from pathlib import Path

DEF_INI  = "/home/palworld/palserver/DefaultPalWorldSettings.ini"
HERE     = Path(__file__).resolve().parent
DEF_DATA = HERE / "../../data/palworld-settings.json"


def parse_option_settings(text: str) -> dict:
    """Parse the OptionSettings=(...) line into {key: raw_value_string}.
    Handles quoted strings (commas/parens inside quotes) and nested
    tuples like CrossplayPlatforms=(Steam,Xbox,PS5,Mac)."""
    m = re.search(r"^OptionSettings=\((.*)\)\s*$", text, re.MULTILINE)
    if not m:
        sys.exit("ERROR: no single-line OptionSettings=(...) found — file may "
                 "be malformed (which itself is a finding!).")
    body = m.group(1)
    pairs, buf, depth, in_q = [], "", 0, False
    for ch in body:
        if ch == '"':
            in_q = not in_q
        elif not in_q:
            if ch == '(':
                depth += 1
            elif ch == ')':
                depth -= 1
            elif ch == ',' and depth == 0:
                pairs.append(buf); buf = ""
                continue
        buf += ch
    if buf:
        pairs.append(buf)
    out = {}
    for p in pairs:
        if '=' not in p:
            continue
        k, v = p.split('=', 1)
        out[k.strip()] = v.strip()
    return out


def norm_ini(raw: str, typ: str):
    """Normalize a raw ini value for comparison, guided by our declared type."""
    if typ == "bool":
        return raw.lower() == "true"
    if typ in ("float", "int"):
        try:
            f = float(raw)
            return int(f) if typ == "int" and f == int(f) else f
        except ValueError:
            return raw
    if typ == "string":
        return raw.strip('"')
    if typ == "enum":
        return raw.strip('"')
    if typ == "list":
        return raw  # compared raw; lists are eyeballed
    return raw


def norm_ours(default, typ: str):
    if typ == "int" and isinstance(default, float) and default == int(default):
        return int(default)
    if typ == "float" and isinstance(default, int):
        return float(default)
    return default


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--ini",  default=DEF_INI,        help="path to DefaultPalWorldSettings.ini")
    ap.add_argument("--data", default=str(DEF_DATA),  help="path to palworld-settings.json")
    args = ap.parse_args()

    ini_path = Path(args.ini)
    data_path = Path(args.data).resolve()
    if not ini_path.exists():
        sys.exit(f"ERROR: {ini_path} not found. Is the server installed? (run with sudo?)")
    if not data_path.exists():
        sys.exit(f"ERROR: {data_path} not found. Run from the repo clone, or pass --data.")

    ini = parse_option_settings(ini_path.read_text(errors="replace"))
    data = json.loads(data_path.read_text())
    ours = {k["key"]: k for k in data["keys"]}

    ini_keys, our_keys = set(ini), set(ours)

    missing_from_data = sorted(ini_keys - our_keys)          # game has, we don't
    not_in_template   = sorted(our_keys - ini_keys)          # we have, game doesn't
    mismatches, matches = [], 0

    for k in sorted(ini_keys & our_keys):
        entry = ours[k]
        got = norm_ini(ini[k], entry["type"])
        want = norm_ours(entry["default"], entry["type"])
        if entry["type"] == "list":
            same = re.sub(r"\s", "", str(got)) == re.sub(r"\s", "", str(want))
        else:
            same = got == want
        if same:
            matches += 1
        else:
            mismatches.append({"key": k, "ours": entry["default"],
                               "template": ini[k], "type": entry["type"]})

    flagged = [k for k in data["keys"] if k.get("verify")]
    verdicts = []
    for e in flagged:
        k = e["key"]
        if k not in ini:
            verdicts.append({"key": k, "flags": e["verify"],
                             "verdict": "KEY NOT IN TEMPLATE — investigate (renamed? runtime-only? wrong?)"})
        else:
            got = norm_ini(ini[k], e["type"])
            want = norm_ours(e["default"], e["type"])
            if got == want:
                verdicts.append({"key": k, "flags": e["verify"],
                                 "verdict": f"CONFIRMED — template says {ini[k]!r}, matches our default"})
            else:
                verdicts.append({"key": k, "flags": e["verify"],
                                 "verdict": f"CORRECTION NEEDED — template says {ini[k]!r}, we say {e['default']!r}"})

    W = 74
    def hdr(t): print("\n" + "=" * W + f"\n {t}\n" + "=" * W)

    print(f"Template : {ini_path}  ({len(ini_keys)} keys)")
    print(f"Data file: {data_path}  ({len(our_keys)} keys)")
    print(f"Matched defaults: {matches}/{len(ini_keys & our_keys)} shared keys")

    hdr(f"A. IN TEMPLATE BUT MISSING FROM OUR DATA FILE ({len(missing_from_data)}) — add these")
    for k in missing_from_data:
        print(f"  {k} = {ini[k]}")

    hdr(f"B. IN OUR DATA FILE BUT NOT IN TEMPLATE ({len(not_in_template)}) — investigate each")
    for k in not_in_template:
        src = ours[k]["source"]
        print(f"  {k}  (source: {src})")

    hdr(f"C. DEFAULT MISMATCHES ({len(mismatches)}) — template wins unless proven otherwise")
    for m in mismatches:
        print(f"  {m['key']}: ours={m['ours']!r}  template={m['template']!r}")

    hdr(f"D. VERDICTS FOR THE {len(flagged)} verify-FLAGGED ENTRIES")
    for v in verdicts:
        print(f"  {v['key']} [{','.join(v['flags'])}]:\n      {v['verdict']}")

    report = {"template_path": str(ini_path), "template_key_count": len(ini_keys),
              "data_key_count": len(our_keys),
              "missing_from_data": {k: ini[k] for k in missing_from_data},
              "not_in_template": not_in_template,
              "default_mismatches": mismatches, "verify_verdicts": verdicts}
    out = HERE / "verify-report.json"
    out.write_text(json.dumps(report, indent=2))
    print(f"\nMachine-readable report written to: {out}")
    print("Paste that file (or this output) back into the Claude chat to get "
          "the corrected data file.")


if __name__ == "__main__":
    main()
