// Command paladin — trial CLI wiring every internal package into live
// maintenance cycles against a real server. This is the assembly step
// before the web UI: webserv will call the same wiring.
//
// RUN AS THE SERVICE ACCOUNT (Model A, DESIGN.md §5.2): all file work
// happens natively as the palworld user (no sudo, no chown), and the ONE
// privileged operation — systemctl on the server's unit — goes through a
// narrowly-scoped sudoers grant (deploy/grants/palworld-paladin.sudoers).
// Run it as:  sudo -u palworld ./paladin <cmd>   (with the grant installed)
//
// Usage (testbox defaults built in):
//
//	paladin status
//	paladin backup create | list | prune --keep N
//	paladin commit --set ExpRate=2 --set bEnableVoiceChat=true [--countdown 30]
//	paladin restore --backup <id> [--countdown 30]
//	paladin recover
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/juicetheforce/palworld-paladin/internal/backup"
	"github.com/juicetheforce/palworld-paladin/internal/events"
	"github.com/juicetheforce/palworld-paladin/internal/hostmetrics"
	"github.com/juicetheforce/palworld-paladin/internal/maintain"
	"github.com/juicetheforce/palworld-paladin/internal/palapi"
	"github.com/juicetheforce/palworld-paladin/internal/roster"
	"github.com/juicetheforce/palworld-paladin/internal/sav"
	"github.com/juicetheforce/palworld-paladin/internal/settings"
	"github.com/juicetheforce/palworld-paladin/internal/steam"
	"github.com/juicetheforce/palworld-paladin/internal/supervise"
	"github.com/juicetheforce/palworld-paladin/internal/update"
	"github.com/juicetheforce/palworld-paladin/internal/webserv"
)

// version is stamped by release builds via -ldflags "-X main.version=vX.Y.Z".
var version = "dev"

// defaults matching deploy/testbox/bootstrap-palworld-testbox.sh
const (
	defUnit       = "palserver.service"
	defAPIURL     = "http://127.0.0.1:8212"
	defCredsFile  = "/home/palworld/palserver-credentials.txt"
	defINI        = "/home/palworld/palserver/Pal/Saved/Config/LinuxServer/PalWorldSettings.ini"
	defSavesRoot  = "/home/palworld/palserver/Pal/Saved/SaveGames/0"
	defBackups    = "/home/palworld/paladin-backups"
	defJournal    = "/home/palworld/paladin-journal"
	defSafetyHold = "/home/palworld/paladin-safety" // relocated pre-restore copies live here
	defAuthFile   = "/home/palworld/paladin-config/auth.json"
	defMemRestart = "/home/palworld/paladin-config/memrestart.json"
	defEventLog   = "/home/palworld/paladin-logs/events.jsonl"
	defWebAddr    = "127.0.0.1:8080"
)

type deps struct {
	cfg      AppConfig
	api      *palapi.Client
	unit     *supervise.UnitController
	fj       *maintain.FileJournal
	mgr      *backup.Manager
	keyList  *settings.KeyList
	worldDir string
	iniPath  string
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "status":
		err = cmdStatus(args)
	case "backup":
		err = cmdBackup(args)
	case "commit":
		err = cmdCommit(args)
	case "restore":
		err = cmdRestore(args)
	case "recover":
		err = cmdRecover(args)
	case "serve":
		err = cmdServe(args)
	case "version":
		fmt.Println("paladin", version)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `paladin `+version+` — Palworld server maintenance (trial CLI)
  status                         server, unit, and metrics at a glance
  backup create|list|prune       manage the backup catalog
  commit --set Key=Value ...     staged settings commit-and-restart cycle
  restore --backup <id>          orchestrated world restore cycle
  recover                        report a crash-interrupted cycle, if any
  serve [--addr host:port]       run the web UI (default 127.0.0.1:8080)`)
}

// ---- wiring -----------------------------------------------------------------

// resolveConfig: explicit path > PALADIN_CONFIG env > default location.
func resolveConfig(explicit string) (AppConfig, error) {
	if explicit == "" {
		explicit = os.Getenv("PALADIN_CONFIG")
	}
	return loadAppConfig(explicit)
}

func build(cfg AppConfig) (*deps, error) {
	d := &deps{cfg: cfg, iniPath: cfg.iniPath()}
	pw, err := cfg.adminPassword()
	if err != nil {
		return nil, err
	}
	d.api = palapi.New(cfg.apiURL(), pw)
	d.unit = supervise.NewScopedUnitController(cfg.serverUnit())

	// Detect the world folder — never assume (§7.4 ethos).
	des, err := os.ReadDir(cfg.savesRoot())
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", cfg.savesRoot(), err)
	}
	var worlds []string
	for _, de := range des {
		// Skip dot-prefixed entries: these are Paladin's own scratch
		// (e.g. .paladin-safety-* during/after a restore), never worlds.
		if de.IsDir() && !strings.HasPrefix(de.Name(), ".") {
			worlds = append(worlds, de.Name())
		}
	}
	if len(worlds) != 1 {
		return nil, fmt.Errorf("expected exactly one world under %s, found %v", defSavesRoot, worlds)
	}
	d.worldDir = filepath.Join(defSavesRoot, worlds[0])

	d.fj, err = maintain.NewFileJournal(cfg.journalDir())
	if err != nil {
		return nil, err
	}
	d.mgr, err = backup.NewManager(cfg.backupsDir())
	if err != nil {
		return nil, err
	}
	d.keyList, err = settings.LoadKeyList()
	if err != nil {
		return nil, err
	}
	return d, nil
}

// noopSusp: no daemon supervisor exists yet in the trial CLI; the systemd
// unit's Restart=on-failure ignores deliberate stops, so cycles are safe.
type noopSusp struct{}

func (noopSusp) Suspend() {}
func (noopSusp) Resume()  {}

func engineFor(d *deps, countdown int) (*maintain.Engine, error) {
	var ann []maintain.Announcement
	if countdown > 0 {
		ann = append(ann, maintain.Announcement{
			Message: fmt.Sprintf("[Paladin] Server maintenance in %d seconds — please reach a safe spot.", countdown),
			Wait:    time.Duration(countdown) * time.Second,
		})
	}
	ann = append(ann, maintain.Announcement{Message: "[Paladin] Maintenance starting now.", Wait: 2 * time.Second})
	return maintain.NewEngine(maintain.Config{
		API: d.api, Unit: d.unit, Susp: noopSusp{}, Journal: d.fj,
		OnEvent:       printEvent,
		Announcements: ann,
		DiskCheck:     backup.DiskCheckFunc(d.worldDir, 2.0),
		StopDecider:   terminalStopDialog,
	})
}

func printEvent(e maintain.Event) {
	ts := e.Time.Format("15:04:05")
	switch e.Kind {
	case maintain.EventStepStarted:
		fmt.Printf("%s  [%s] %-9s …\n", ts, e.Payload, e.Step)
	case maintain.EventStepOK:
		fmt.Printf("%s  [%s] %-9s ok\n", ts, e.Payload, e.Step)
	case maintain.EventStepFailed:
		fmt.Printf("%s  [%s] %-9s FAILED: %s\n", ts, e.Payload, e.Step, e.Detail)
	case maintain.EventRolledBack:
		fmt.Printf("%s  [%s] %-9s rolling back: %s\n", ts, e.Payload, e.Step, e.Detail)
	case maintain.EventAwaitingOp:
		fmt.Printf("%s  [%s] %-9s AWAITING OPERATOR\n", ts, e.Payload, e.Step)
	case maintain.EventDoubleFault:
		fmt.Printf("%s  [%s] %-9s DOUBLE FAULT: %s\n", ts, e.Payload, e.Step, e.Detail)
	case maintain.EventCycleFinished:
		fmt.Printf("%s  [%s] finished: %s\n", ts, e.Payload, e.Detail)
	}
}

// terminalStopDialog is the §6.9 STOP escalation, terminal edition.
func terminalStopDialog(ctx context.Context) (maintain.StopDecision, error) {
	fmt.Println()
	fmt.Println("The server didn't shut down within the grace window. It may be hung, or just slow.")
	fmt.Println("The world was already saved at the start of this job, so a force kill will NOT lose")
	fmt.Println("recent progress — it ends the process immediately instead of waiting.")
	fmt.Println()
	fmt.Println("  [k] Force kill and continue the job")
	fmt.Println("  [c] Cancel the job (nothing on disk was touched; the server may need attention)")
	fmt.Print("choice [k/c]: ")
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		switch strings.ToLower(strings.TrimSpace(sc.Text())) {
		case "k":
			return maintain.DecisionForceKill, nil
		case "c":
			return maintain.DecisionCancel, nil
		default:
			fmt.Print("please answer k or c: ")
		}
	}
	return maintain.DecisionCancel, sc.Err()
}

func printOutcome(out maintain.Outcome) {
	fmt.Println()
	fmt.Println("outcome:", out.Status)
	if out.FailedStep != "" {
		fmt.Println("at step:", out.FailedStep)
	}
	if out.Detail != "" {
		fmt.Println("detail: ", out.Detail)
	}
	for _, w := range out.VerifyIssues {
		fmt.Println("  ⚠ ", w)
	}
	for _, n := range out.VerifyNotes {
		fmt.Println("  ℹ ", n)
	}
	if len(out.Anchors) > 0 {
		fmt.Println("recovery anchors:")
		for _, a := range out.Anchors {
			fmt.Println("  →", a)
		}
	}
}

func refuseIfUnclosed() error {
	cfg, err := resolveConfig("")
	if err != nil {
		return err
	}
	u, err := maintain.ReadUnclosed(cfg.journalDir())
	if err != nil {
		return err
	}
	if u != nil {
		return fmt.Errorf("a previous cycle (%s, %s) was interrupted at step %s — run 'paladin recover' and resolve before starting a new cycle",
			u.CycleID, u.Kind, u.LastStep)
	}
	return nil
}

// ---- commands ---------------------------------------------------------------

func cmdStatus(args []string) error {
	cfg, err := resolveConfig("")
	if err != nil {
		return err
	}
	d, err := build(cfg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	info, err := d.api.Info(ctx)
	if err != nil {
		return fmt.Errorf("REST: %w", err)
	}
	m, _ := d.api.Metrics(ctx)
	p, err := d.unit.Show(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("server   %s  %q  world %s\n", info.Version, info.ServerName, info.WorldGUID)
	fmt.Printf("unit     %s: %s/%s, memory %.1f MiB\n", defUnit, p.ActiveState, p.SubState,
		float64(p.MemoryCurrent)/(1<<20))
	if m != nil {
		fmt.Printf("metrics  players %d/%d, bases %d, fps %.0f (avg %.1f), uptime %ds, day %d\n",
			m.CurrentPlayerNum, m.MaxPlayerNum, m.BaseCampNum, m.ServerFPS, m.ServerFPSAverage,
			m.Uptime, m.Days)
	}
	fmt.Printf("world    %s\n", d.worldDir)
	entries, partials, _ := d.mgr.List()
	fmt.Printf("backups  %d in catalog", len(entries))
	if len(partials) > 0 {
		fmt.Printf("  (%d PARTIAL leftovers from interrupted backups — inspect them)", len(partials))
	}
	fmt.Println()
	return nil
}

func cmdBackup(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("backup: need create|list|prune")
	}
	cfg, err := resolveConfig("")
	if err != nil {
		return err
	}
	d, err := build(cfg)
	if err != nil {
		return err
	}
	switch args[0] {
	case "create":
		fmt.Println("saving world via REST before copy …")
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := d.api.Save(ctx); err != nil {
			return err
		}
		e, err := d.mgr.Create(ctx, d.worldDir, backup.TriggerManual)
		if err != nil {
			return err
		}
		fmt.Printf("created %s (%.2f MiB)\n", e.ID, float64(e.TotalSize)/(1<<20))
		return nil
	case "list":
		entries, partials, err := d.mgr.List()
		if err != nil {
			return err
		}
		for _, e := range entries {
			fmt.Printf("%-40s %-12s %8.2f MiB  %s\n", e.ID, e.Trigger,
				float64(e.TotalSize)/(1<<20), e.Created.Format(time.RFC3339))
		}
		for _, pp := range partials {
			fmt.Println("PARTIAL (interrupted, inspect):", pp)
		}
		if len(entries) == 0 && len(partials) == 0 {
			fmt.Println("no backups yet")
		}
		return nil
	case "prune":
		fs := flag.NewFlagSet("prune", flag.ContinueOnError)
		keep := fs.Int("keep", 10, "backups to keep")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		deleted, err := d.mgr.Prune(*keep)
		if err != nil {
			return err
		}
		fmt.Printf("pruned %d: %v\n", len(deleted), deleted)
		return nil
	}
	return fmt.Errorf("backup: unknown subcommand %q", args[0])
}

type setFlags []string

func (s *setFlags) String() string     { return strings.Join(*s, ",") }
func (s *setFlags) Set(v string) error { *s = append(*s, v); return nil }

func cmdCommit(args []string) error {
	fs := flag.NewFlagSet("commit", flag.ContinueOnError)
	var sets setFlags
	fs.Var(&sets, "set", "Key=Value (repeatable)")
	countdown := fs.Int("countdown", 30, "seconds of warning before stopping (0 = brief notice only)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(sets) == 0 {
		return fmt.Errorf("commit: at least one --set Key=Value required")
	}
	if err := refuseIfUnclosed(); err != nil {
		return err
	}
	cfg, err := resolveConfig("")
	if err != nil {
		return err
	}
	d, err := build(cfg)
	if err != nil {
		return err
	}

	staged := map[string]any{}
	for _, kv := range sets {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("--set %q: want Key=Value", kv)
		}
		def, found := d.keyList.Lookup(k)
		if !found {
			return fmt.Errorf("--set %s: unknown key", k)
		}
		val, err := settings.ParseValue(def, v)
		if err != nil {
			return err
		}
		staged[def.Key] = val
	}

	p := &settings.CommitPayload{
		KeyList: d.keyList, INIPath: d.iniPath, WorldDir: d.worldDir,
		Staged:       staged,
		WorldBackup:  backup.WorldBackupFunc(d.mgr, d.worldDir),
		ReadSettings: d.api.Settings,
		BackupAnchor: d.cfg.backupsDir(),
	}
	eng, err := engineFor(d, *countdown)
	if err != nil {
		return err
	}
	fmt.Printf("commit cycle: %d staged key(s), %ds countdown\n", len(staged), *countdown)
	out, err := eng.Run(context.Background(), cycleID("commit"), p)
	printOutcome(out)
	if err != nil && !errors.Is(err, maintain.ErrBusy) {
		return err
	}
	return nil
}

func cmdRestore(args []string) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	id := fs.String("backup", "", "backup id (from 'paladin backup list')")
	countdown := fs.Int("countdown", 30, "seconds of warning before stopping")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("restore: --backup <id> required")
	}
	if err := refuseIfUnclosed(); err != nil {
		return err
	}
	cfg, err := resolveConfig("")
	if err != nil {
		return err
	}
	d, err := build(cfg)
	if err != nil {
		return err
	}

	entry, err := d.mgr.Get(*id)
	if err != nil {
		return err
	}
	p := &backup.RestorePayload{
		Mgr: d.mgr, Selected: entry, WorldDir: d.worldDir,
		SafetyRelocateDir: d.cfg.safetyDir(), // relocate out of the save tree after success (§2)
		ReadWorldGUID: func(ctx context.Context) (string, error) {
			info, err := d.api.Info(ctx)
			if err != nil {
				return "", err
			}
			return info.WorldGUID, nil
		},
	}
	eng, err := engineFor(d, *countdown)
	if err != nil {
		return err
	}
	fmt.Printf("restore cycle: backup %s (%s, %.2f MiB), %ds countdown\n",
		entry.ID, entry.Trigger, float64(entry.TotalSize)/(1<<20), *countdown)
	out, err := eng.Run(context.Background(), cycleID("restore"), p)
	printOutcome(out)
	if err != nil && !errors.Is(err, maintain.ErrBusy) {
		return err
	}
	return nil
}

func cmdRecover(args []string) error {
	cfg, err := resolveConfig("")
	if err != nil {
		return err
	}
	u, err := maintain.ReadUnclosed(cfg.journalDir())
	if err != nil {
		return err
	}
	if u == nil {
		fmt.Println("no interrupted cycle; journal is clean")
		return nil
	}
	fmt.Printf("INTERRUPTED CYCLE: %s (%s)\n", u.CycleID, u.Kind)
	fmt.Printf("last step in flight: %s\n", u.LastStep)
	fmt.Println("journal entries:")
	for _, e := range u.Entries {
		fmt.Printf("  %s  %-12s %-10s %s\n", e.Time.Format(time.RFC3339), e.Event, e.Step, e.Detail)
	}
	fmt.Println()
	fmt.Println("Nothing is resumed automatically (§6.9 I3). Inspect the anchors on disk")
	fmt.Println("(pre-write ini copy, backups, any *.paladin-safety-* world folder), restore")
	fmt.Println("by hand if needed, then delete " + filepath.Join(cfg.journalDir(), "active.journal") +
		" to acknowledge.")
	return nil
}

func cycleID(kind string) string {
	return kind + "-" + time.Now().UTC().Format("20060102T150405Z")
}

// cmdServe runs Paladin's web UI. Read-only dashboard slice: auth +
// /api/status over the embedded React bundle (DESIGN.md §6.6).
func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "", "listen address (host:port); overrides config; default is localhost-only")
	cfgPath := fs.String("config", "", "deployment config file (default $PALADIN_CONFIG or "+defConfigPath+")")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := resolveConfig(*cfgPath)
	if err != nil {
		return err
	}
	d, err := build(cfg)
	if err != nil {
		return err
	}
	if *addr == "" {
		*addr = cfg.listen()
	}
	auth, err := webserv.LoadAuthStore(cfg.authFile())
	if err != nil {
		return err
	}
	sampler := hostmetrics.NewSampler(3 * time.Second)
	go sampler.Run(context.Background())

	// Live-event hub + log tailer. NOTE (docs rev 17): the Linux shipping
	// build writes no Pal.log even with -log — this tailer is deliberately
	// dormant machinery that self-activates if a future build ever logs.
	// Player events come from the roster differ, not from here.
	hub := events.NewHub(512)
	logPath := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(d.worldDir))), "Logs", "Pal.log")
	go events.TailLog(context.Background(), hub, logPath)

	// banlist.txt lives in SaveGames/ (parent of the world dirs).
	banlistPath := filepath.Join(filepath.Dir(d.worldDir), "banlist.txt")

	// Memory-threshold auto-restart (§6.1 rev 16): the supervisor watches
	// the game unit's cgroup memory and fires a graceful restart when the
	// operator-configured threshold trips. recordAction is late-bound: the
	// action logs to the history card, but the web server is constructed
	// after the supervisor — the indirection resolves at fire time.
	var recordAction func(action, detail string, ok bool)
	memCfg := func() supervise.RestartConfig { return supervise.RestartConfig{} }
	sup := supervise.NewSupervisor(d.unit, supervise.Config{
		OnEvent: func(e supervise.Event) {
			if e.Kind == supervise.EventRAMThresholdExceeded {
				hub.Progress("mem-restart", "trigger",
					fmt.Sprintf("Memory threshold exceeded (%.1f GB in use) — restarting to reclaim it", float64(e.Memory)/float64(1<<30)))
			}
		},
		RestartAction: func(ctx context.Context) {
			runMemRestart(ctx, d, hub, memCfg(), func(a, det string, ok bool) {
				if recordAction != nil {
					recordAction(a, det, ok)
				}
			})
		},
	})
	go sup.Run(context.Background())

	memStore, err := supervise.LoadRestartConfigStore(cfg.memRestartFile(), func(rc supervise.RestartConfig) {
		sup.SetMemThreshold(rc.ThresholdBytes())
	})
	if err != nil {
		return err
	}
	memCfg = memStore.Get

	// Event persistence (rev 17: the game writes no logs, so Paladin's
	// event history is the only history there is). Seed the hub's ring
	// from the last run's events, then persist ring-worthy events as they
	// happen. A failed open degrades to in-memory-only, loudly.
	if evlog, err := events.OpenEventLog(cfg.eventLogFile(), 1<<20); err != nil {
		fmt.Fprintln(os.Stderr, "warning: event log unavailable ("+err.Error()+") — history will not survive restarts")
	} else {
		if loaded, err := events.LoadRecent(cfg.eventLogFile(), 100); err == nil && len(loaded) > 0 {
			hub.Seed(loaded)
		}
		hub.SetPersist(func(e events.Event) { evlog.Append(e) })
	}

	// Roster differ (rev 16): player join/leave events from REST polling —
	// the launch-flag-independent player-event source. Baseline poll is
	// silent; outages never fabricate events.
	differ := roster.New(d.api.Players,
		func(p palapi.Player) { hub.Player(p.Name + " joined the server") },
		func(p palapi.Player) { hub.Player(p.Name + " left the server") },
	)
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for range t.C {
			pctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			differ.Poll(pctx) // errors = server down; differ handles it
			cancel()
		}
	}()

	// One engine for all web-triggered maintenance cycles (I1: one lock).
	// Step events bridge to the hub; the terminal event is published by
	// each runner with operation-appropriate semantics. The REAL supervisor
	// is the engine's Susp — invariant I2 (no threshold firing mid-cycle)
	// is now live, not a noop.
	serveEngine, err := maintain.NewEngine(maintain.Config{
		API: d.api, Unit: d.unit, Susp: sup, Journal: d.fj,
		OnEvent:   bridgeEngineEvents(hub),
		DiskCheck: backup.DiskCheckFunc(d.worldDir, 2.0),
		UnitActive: func(ctx context.Context) (bool, error) {
			pr, err := d.unit.Show(ctx)
			if err != nil {
				return false, err
			}
			return pr.ActiveState == "active", nil
		},
	})
	if err != nil {
		return err
	}
	updateRunner := makeUpdateRunner(d, serveEngine, hub)

	srv := webserv.New(webserv.Config{
		Auth:        auth,
		Sessions:    webserv.NewSessionStore(12 * time.Hour),
		Status:      d.api,
		Backups:     d.mgr,
		Host:        sampler,
		Players:     d.api,
		BanList:     func() ([]palapi.BanEntry, error) { return palapi.ReadBanList(banlistPath) },
		Lifecycle:   d.unit,
		Broadcaster: d.api,
		BackupMgr:   d.mgr,
		Readiness:   d.api,
		Update:      updateRunner,
		LocalBuild: func() (string, error) {
			return steam.LocalBuildID(installDirFromWorld(d.worldDir), steam.PalworldAppID)
		},
		RemoteBuild: func(c context.Context) (string, error) {
			scmd, err := steam.FindSteamCMD()
			if err != nil {
				return "", err
			}
			return steam.RemoteBuildID(c, scmd, steam.PalworldAppID)
		},
		MemRestart: memStore,
		CreateBackup: func(ctx context.Context) (*backup.Entry, error) {
			// Save first (best-effort) so the backup captures the latest
			// world state; a down server just skips the save.
			sctx, cancel := context.WithTimeout(ctx, 60*time.Second)
			d.api.Save(sctx)
			cancel()
			return d.mgr.Create(ctx, d.worldDir, backup.TriggerManual)
		},
		Restore: makeRestoreRunner(d, serveEngine, hub),
		KeyList: d.keyList,
		SettingsValues: func() (map[string]string, error) {
			ini, err := settings.LoadINIFile(d.iniPath)
			if err != nil {
				return nil, err
			}
			vals := make(map[string]string)
			for _, k := range ini.Keys() {
				if v, ok := ini.Get(k); ok {
					vals[k] = v
				}
			}
			return vals, nil
		},
		Commit: makeCommitRunner(d, serveEngine, hub),
		UnitMemory: func(ctx context.Context) (uint64, error) {
			p, err := d.unit.Show(ctx)
			if err != nil {
				return 0, err
			}
			return p.MemoryCurrent, nil
		},
		LogTail:      func(n int) ([]string, error) { return events.TailFile(logPath, n) },
		GameTime:     cachedGameTime(d, 20*time.Second),
		Actors:       d.api.Actors,
		MapImagePath: cfg.worldMapFile(),
		World:        makeWorldFunc(d),
		Hub:          hub,
		Static:       webserv.Assets(),
	})
	recordAction = srv.RecordAction
	if auth.NeedsSetup() {
		fmt.Println("First run: open the web UI to create your admin password.")
	}
	fmt.Printf("Paladin web UI on http://%s  (LAN/localhost only — do not expose publicly)\n", *addr)
	fmt.Printf("  subsystems: live-events=on log-tail=%q host-metrics=on\n", logPath)
	return http.ListenAndServe(*addr, srv.Handler())
}

// bridgeEngineEvents adapts maintain engine step events into hub events
// for the live viewer. CycleFinished is deliberately NOT bridged — each
// runner publishes its own terminal event with the right semantics (e.g.
// "already up to date" is a friendly no-op, not a red failure).
func bridgeEngineEvents(hub *events.Hub) func(maintain.Event) {
	return func(e maintain.Event) {
		step := strings.ToLower(string(e.Step))
		switch e.Kind {
		case maintain.EventStepStarted:
			hub.Progress(e.Payload, step, stepLabel(step)+"…")
		case maintain.EventStepOK:
			hub.Progress(e.Payload, step, stepLabel(step)+" ok")
		case maintain.EventStepFailed:
			hub.Error(e.Payload, stepLabel(step)+" FAILED: "+e.Detail)
		case maintain.EventRolledBack:
			hub.Progress(e.Payload, step, "rolling back: "+e.Detail)
		case maintain.EventAwaitingOp:
			hub.Error(e.Payload, stepLabel(step)+": awaiting operator decision")
		}
	}
}

func stepLabel(step string) string {
	switch step {
	case "pre-check", "precheck":
		return "Pre-check"
	case "announce":
		return "Announcing"
	case "save":
		return "Saving world"
	case "stop":
		return "Stopping server"
	case "backup":
		return "Backing up world"
	case "apply":
		return "Applying"
	case "start":
		return "Starting server"
	case "verify":
		return "Verifying"
	}
	return step
}

// makeUpdateRunner builds the closure the web layer calls to run one full
// server-update cycle. Steam deps are resolved per run (steamcmd path can
// change; a missing steamcmd is a clean pre-check error, not a crash).
// installDirFromWorld derives the server install root from the world dir:
// worldDir is <install>/Pal/Saved/SaveGames/0/<GUID>, so the install dir is
// five levels up. The appmanifest lives at
// <install>/steamapps/appmanifest_2394010.acf (force_install_dir layout,
// which is how Paladin installs and adopts).
func installDirFromWorld(worldDir string) string {
	installDir := worldDir
	for i := 0; i < 5; i++ {
		installDir = filepath.Dir(installDir)
	}
	return installDir
}

func makeUpdateRunner(d *deps, eng *maintain.Engine, hub *events.Hub) webserv.UpdateRunner {
	installDir := installDirFromWorld(d.worldDir)

	return func(ctx context.Context, broadcast string, delaySec int) webserv.UpdateResult {
		steamcmd, err := steam.FindSteamCMD()
		if err != nil {
			hub.Error("update", err.Error())
			return webserv.UpdateResult{Status: "aborted", Detail: err.Error()}
		}

		p, err := update.New(update.Deps{
			LocalBuildID:  func() (string, error) { return steam.LocalBuildID(installDir, steam.PalworldAppID) },
			RemoteBuildID: func(c context.Context) (string, error) { return steam.RemoteBuildID(c, steamcmd, steam.PalworldAppID) },
			RunUpdate: func(c context.Context, onLine func(string)) error {
				return steam.RunUpdate(c, steamcmd, installDir, steam.PalworldAppID, onLine)
			},
			GameVersion: func(c context.Context) (string, error) {
				info, err := d.api.Info(c)
				if err != nil {
					return "", err
				}
				return info.Version, nil
			},
			Backup: func(c context.Context) (string, error) {
				e, err := d.mgr.Create(c, d.worldDir, backup.TriggerPreUpdate)
				if err != nil {
					return "", err
				}
				return e.Path, nil
			},
			OnLine: hub.Log,
		})
		if err != nil {
			return webserv.UpdateResult{Status: "aborted", Detail: err.Error()}
		}

		var ann []maintain.Announcement
		if broadcast != "" {
			wait := time.Duration(delaySec) * time.Second
			ann = append(ann, maintain.Announcement{Message: broadcast, Wait: wait})
		}

		hub.Progress("update", "check", "Checking Steam for a server update…")
		cycleID := fmt.Sprintf("update-%d", time.Now().Unix())
		out, _ := eng.RunWithAnnouncements(context.Background(), cycleID, p, ann)

		// Terminal event with operation-appropriate semantics.
		switch {
		case p.UpToDate:
			hub.Done("update", "Server is already up to date — nothing to do.", true)
		case out.Status == maintain.StatusSuccess:
			hub.Done("update", "Update complete — server is back up and verified.", true)
		case out.Status == maintain.StatusSuccessWithWarnings:
			hub.Done("update", "Update applied and server is up, with warnings: "+out.Detail, true)
		default:
			hub.Error("update", "Update did not complete: "+out.Detail)
		}
		return webserv.UpdateResult{Status: string(out.Status), Detail: out.Detail, UpToDate: p.UpToDate}
	}
}

// runMemRestart is the graceful action fired when the memory threshold
// trips: (optional) warn players and wait, always force a world save, then
// restart and confirm readiness. The graceful-vs-immediate choice is
// carried entirely by the config fields (§6.1 rev 16: empty broadcast and
// zero delay = immediate).
func runMemRestart(ctx context.Context, d *deps, hub *events.Hub, cfg supervise.RestartConfig, record func(string, string, bool)) {
	op := "mem-restart"
	fail := func(stage string, err error) {
		hub.Error(op, stage+" failed: "+err.Error())
		record("memory restart", stage+" failed: "+err.Error(), false)
	}

	if cfg.Broadcast != "" {
		bctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := d.api.Announce(bctx, cfg.Broadcast); err == nil {
			hub.Progress(op, "announce", "Warned players: "+cfg.Broadcast)
		}
		cancel()
		if cfg.DelaySeconds > 0 {
			hub.Progress(op, "delay", fmt.Sprintf("Waiting %ds before restart…", cfg.DelaySeconds))
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(cfg.DelaySeconds) * time.Second):
			}
		}
	}

	// Always save first: nobody loses progress to a leak restart.
	hub.Progress(op, "save", "Saving world…")
	sctx, scancel := context.WithTimeout(ctx, 60*time.Second)
	err := d.api.Save(sctx)
	scancel()
	if err != nil {
		// Report but continue: a restart that reclaims memory is still
		// better than a server about to OOM; the save failure is loud.
		hub.Error(op, "world save failed (continuing with restart): "+err.Error())
	}

	hub.Progress(op, "restart", "Restarting server to reclaim memory…")
	rctx, rcancel := context.WithTimeout(ctx, 90*time.Second)
	err = d.unit.Restart(rctx)
	rcancel()
	if err != nil {
		fail("restart", err)
		return
	}

	hub.Progress(op, "start", "Waiting for server to respond…")
	wctx, wcancel := context.WithTimeout(ctx, 5*time.Minute)
	err = d.api.WaitReady(wctx, 2*time.Second)
	wcancel()
	if err != nil {
		fail("readiness", err)
		return
	}
	hub.Done(op, "Memory restart complete — server is back up.", true)
	record("memory restart", "threshold restart completed, server responding", true)
}

// makeRestoreRunner builds the closure for web-triggered restore cycles,
// mirroring makeUpdateRunner: same engine (I1 single-flight), same event
// bridge, terminal semantics owned here.
func makeRestoreRunner(d *deps, eng *maintain.Engine, hub *events.Hub) webserv.RestoreRunner {
	return func(ctx context.Context, backupID, broadcast string, delaySec int) webserv.RestoreResult {
		entry, err := d.mgr.Get(backupID)
		if err != nil {
			hub.Error("restore", "backup not found: "+err.Error())
			return webserv.RestoreResult{Status: "aborted", Detail: "backup not found: " + err.Error()}
		}

		p := &backup.RestorePayload{
			Mgr: d.mgr, Selected: entry, WorldDir: d.worldDir,
			SafetyRelocateDir: defSafetyHold,
			ReadWorldGUID: func(c context.Context) (string, error) {
				info, err := d.api.Info(c)
				if err != nil {
					return "", err
				}
				return info.WorldGUID, nil
			},
		}

		var ann []maintain.Announcement
		if broadcast != "" {
			ann = append(ann, maintain.Announcement{Message: broadcast, Wait: time.Duration(delaySec) * time.Second})
		}

		cycleID := fmt.Sprintf("restore-%d", time.Now().Unix())
		// TolerateStopped: restoring onto a DOWN server is the disaster-
		// recovery case (corrupt world preventing startup). The engine
		// skips announce/save/stop when the unit is confirmed inactive,
		// and still refuses a wedged (active but unreachable) process.
		out, _ := eng.RunCycle(context.Background(), cycleID, p, maintain.RunOpts{
			Announcements: ann, TolerateStopped: true,
		})

		switch out.Status {
		case maintain.StatusSuccess:
			hub.Done("restore", "Restore complete — world "+backupID+" is live and verified.", true)
		case maintain.StatusSuccessWithWarnings:
			hub.Done("restore", "Restore applied and server is up, with warnings: "+out.Detail, true)
		default:
			hub.Error("restore", "Restore did not complete: "+out.Detail)
		}
		return webserv.RestoreResult{Status: string(out.Status), Detail: out.Detail}
	}
}

// makeCommitRunner builds the closure for web-triggered settings commits —
// the transactional commit-and-restart cycle (§6.3), same engine and event
// bridge as update and restore.
func makeCommitRunner(d *deps, eng *maintain.Engine, hub *events.Hub) webserv.CommitRunner {
	return func(ctx context.Context, staged map[string]any, broadcast string, delaySec int) webserv.CommitResult {
		p := &settings.CommitPayload{
			KeyList: d.keyList, INIPath: d.iniPath, WorldDir: d.worldDir,
			Staged:       staged,
			WorldBackup:  backup.WorldBackupFunc(d.mgr, d.worldDir),
			ReadSettings: d.api.Settings,
			BackupAnchor: d.cfg.backupsDir(),
		}

		var ann []maintain.Announcement
		if broadcast != "" {
			ann = append(ann, maintain.Announcement{Message: broadcast, Wait: time.Duration(delaySec) * time.Second})
		}

		cycleID := fmt.Sprintf("commit-%d", time.Now().Unix())
		out, _ := eng.RunCycle(context.Background(), cycleID, p, maintain.RunOpts{Announcements: ann})

		switch out.Status {
		case maintain.StatusSuccess:
			hub.Done("commit", fmt.Sprintf("Settings applied — %d change(s) live and verified.", len(staged)), true)
		case maintain.StatusSuccessWithWarnings:
			hub.Done("commit", "Settings applied and server is up, with notes: "+out.Detail, true)
		default:
			hub.Error("commit", "Commit did not complete: "+out.Detail)
		}
		return webserv.CommitResult{Status: string(out.Status), Detail: out.Detail}
	}
}

// cachedGameTime wraps the in-game clock read (/game-data, rev 17) with a
// short cache: the status endpoint is polled every few seconds by several
// views, and game-data returns the full actor list each call — too heavy
// to re-fetch per poll just for a clock. Graceful absence: a server
// launched without -enable-gamedata-api reports ok=false and the card
// simply omits the line.
func cachedGameTime(d *deps, ttl time.Duration) func(ctx context.Context) (string, int, bool) {
	var mu sync.Mutex
	var at time.Time
	var t string
	var days int
	var ok bool
	return func(ctx context.Context) (string, int, bool) {
		mu.Lock()
		defer mu.Unlock()
		if time.Since(at) < ttl {
			return t, days, ok
		}
		at = time.Now()
		gt, err := d.api.GameTime(ctx)
		if err != nil {
			ok = false
			return "", 0, false
		}
		t, days, ok = gt.InGameTime, gt.InGameDays, true
		return t, days, ok
	}
}

// makeWorldFunc wires the save-parsing sidecar (§6.5 historical tier).
// The CLI is looked up per parse (a cheap stat), so installing the sidecar
// while Paladin runs starts working without a restart; results are cached
// 60s with single-flight so page loads never stack expensive parses.
func makeWorldFunc(d *deps) webserv.WorldFunc {
	cached := &sav.Cached{
		TTL: time.Minute,
		Parse: func(ctx context.Context) (*sav.World, error) {
			cli := d.cfg.SavCLI
			if cli == "" {
				var err error
				if cli, err = sav.FindCLI(); err != nil {
					return nil, err
				}
			}
			return sav.Runner{CLIPath: cli, WorldDir: d.worldDir}.Parse(ctx)
		},
	}
	return cached.Get
}
