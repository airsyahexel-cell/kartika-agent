// KARTIKA-AGENT — Tactical Autonomous Security & Audit CLI
//
// Titik masuk program: bootstrapping engine, pemuatan modul built-in dan
// MCP, mode posko (shell taktis interaktif), serta mode senyap/headless
// (--mission) untuk integrasi CI/CD.
//
// 100% Golang Standard Library — tanpa dependensi pihak ketiga.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"kartika-agent/internal/agent"
	"kartika-agent/internal/config"
	"kartika-agent/internal/llm"
	"kartika-agent/internal/mcp"
	"kartika-agent/internal/tools"
	"kartika-agent/internal/ui"
	"kartika-agent/internal/update"
)

// version adalah versi yang dilaporkan HUD & dipakai /update untuk
// membandingkan rilis. Workflow rilis menimpanya lewat
// -ldflags "-X main.version=<tag>", jadi nilai di sini cukup mengikuti
// tag rilis terakhir yang sudah ditandai di repo.
var version = "1.2.0"

func main() {
	mission := flag.String("mission", "", "Direktif chat — jalankan sekali lalu keluar (mode headless/CI)")
	provider := flag.String("provider", "", "Profil provider AI: ollama|vllm|groq|openai|openrouter|gemini|nvidia")
	noColor := flag.Bool("no-color", false, "Nonaktifkan warna ANSI pada HUD")
	noMCP := flag.Bool("no-mcp", false, "Lewati pemuatan MCP server")
	mcpPath := flag.String("mcp-config", "", "Path file konfigurasi MCP (default: mcp.json)")
	maxSteps := flag.Int("max-steps", 0, "Batas langkah penalaran per penugasan (default 160)")
	var lanjut bool
	var sesiList bool
	flag.BoolVar(&lanjut, "lanjut", false, "Lanjutkan sesi chat posko (opsional: kartika --lanjut <id-sesi>)")
	flag.BoolVar(&sesiList, "sesi", false, "Daftar sesi chat tersimpan")
	flag.BoolVar(&sesiList, "s", false, "Alias --sesi")
	showVersion := flag.Bool("version", false, "Tampilkan versi KARTIKA-AGENT")
	flag.Parse()

	if *showVersion {
		fmt.Printf("KARTIKA-AGENT v%s (TACTICAL CORE) — Golang native stdlib\n", version)
		return
	}

	// kartika -s / --sesi: daftar sesi tersimpan lalu keluar.
	if sesiList {
		listSessions()
		return
	}

	// kartika --lanjut [id]: tanpa id = sesi terakhir; dengan id = sesi itu.
	resumeID := ""
	if lanjut && flag.NArg() > 0 && !strings.HasPrefix(flag.Arg(0), "-") {
		resumeID = flag.Arg(0)
	}

	cfg := config.Load(*provider)
	if *maxSteps > 0 {
		cfg.MaxSteps = *maxSteps
	}
	if *mcpPath != "" {
		cfg.MCPConfig = *mcpPath
	}
	if *mission == "" && !lanjut && flag.NArg() > 0 {
		*mission = strings.Join(flag.Args(), " ")
	}

	ui.SetColor(ui.DetectColor(*noColor))
	ui.SetLocalMode(cfg.IsLocal())
	ui.PrintHeader(version)

	// Registri modul taktis: modul built-in inti.
	reg := tools.NewRegistry()
	tools.RegisterBuiltins(reg, cfg.WriteScope)
	builtinCount := reg.Count()

	// Bridge MCP: baca mcp.json -> spawn child process -> handshake -> tools discovery.
	var mcpClients []*mcp.Client
	if !*noMCP {
		mcpClients = loadMCP(cfg, reg)
	}

	// Ping cepat ke endpoint engine agar status HUD jujur sejak awal.
	engineUp := checkEngine(cfg)
	ui.PrintStatus(cfg.Model, builtinCount, len(mcpClients), engineUp)
	if n := tools.SkillsCount(); n > 0 {
		ui.Info(fmt.Sprintf("PUSTAKA SKILL : %d skill @ %s", n, tools.SkillsRoot()))
	}

	// workDir dipakai /update untuk build ulang dari source: bila program
	// dijalankan dari dalam repo, direktori kerja saat ini adalah akar repo.
	workDir, _ := os.Getwd()

	// Pesan konfigurasi lengkap hanya ditampilkan SEKALI per sesi;
	// kegagalan berikutnya cukup pengingat satu-baris.
	configShown := false
	showConfig := func(reason string) {
		if !configShown {
			ui.PrintConfigHelp(cfg.APIURL, cfg.Model, reason)
			configShown = true
		} else {
			ui.PrintConfigShort()
		}
	}
	if !engineUp {
		showConfig("API atau model belum dikonfigurasi — engine tidak dapat dihubungi.")
	}

	engine := llm.NewClient(cfg.APIURL, cfg.APIKey, cfg.Model, cfg.Temperature, time.Duration(cfg.TimeoutSec)*time.Second)
	ag := agent.New(cfg, engine, reg)

	// Pembersihan sumber daya: matikan seluruh MCP server saat sesi berakhir.
	var shutdownOnce sync.Once
	cleanup := func() {
		shutdownOnce.Do(func() {
			for _, c := range mcpClients {
				c.Shutdown()
			}
		})
	}
	defer cleanup()
	ui.SetInterruptHandler(cleanup) // Ctrl+C dari mode raw tetap bersih-bersih

	// Hook tambahan saat SIGINT (mode posko: simpan sesi + tutup layar penuh).
	var exitHook atomic.Value // func()
	exitHook.Store(func() {})

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		if _, ok := <-sigCh; ok {
			if f, ok2 := exitHook.Load().(func()); ok2 {
				f()
			}
			cleanup()
			os.Exit(130)
		}
	}()

	if *mission != "" {
		// MODE OPERASI SENYAP / HEADLESS — eksekusi tunggal, cocok untuk CI/CD.
		ui.PrintMissionLine(*mission)
		fmt.Println()
		if err := ag.Run(*mission); err != nil {
			reportFailure(err, cfg, func(reason string) {
				ui.PrintConfigHelp(cfg.APIURL, cfg.Model, reason)
			})
			os.Exit(1)
		}
		return
	}

	// MODE POSKO — chat taktis (gaya Claude Code / opencode). Di terminal
	// interaktif kartika membuka LAYAR PENUH miliknya sendiri (alternate
	// screen — kartika terbuka sebagai sebuah "terminal" penuh); pada
	// pipe/CI jatuh ke mode CLI polos.
	//
	// Sesi tersimpan otomatis: setiap giliran selesai, saat /keluar, dan
	// saat Ctrl+C — buka ulang dengan `kartika --lanjut` (atau --lanjut <id>).
	sesiID := newSessionID()
	sesiMulai := time.Now()
	resumeNotice := ""
	if lanjut {
		var msgs []llm.Message
		var err error
		if resumeID != "" {
			sesiID, msgs, err = loadSessionFile(sessionDir() + "/sesi-" + resumeID + ".json")
			if err != nil {
				resumeNotice = fmt.Sprintf("sesi %q tidak ditemukan — jalankan kartika -s untuk daftar sesi", resumeID)
				sesiID = newSessionID()
				msgs = nil
			}
		} else {
			sesiID, msgs, err = loadLatestSession()
		}
		if err == nil && len(msgs) > 0 {
			ag.Restore(msgs)
			sesiMulai = time.Now()
			resumeNotice = fmt.Sprintf("sesi dilanjutkan: %s — %d giliran percakapan diingat", sesiID, len(msgs)/2)
		} else if err == nil && len(msgs) == 0 {
			resumeNotice = "sesi kosong — memulai sesi baru"
			sesiID = newSessionID()
		} else if resumeNotice == "" {
			resumeNotice = "tidak ada sesi tersimpan — memulai sesi baru"
		}
	}
	// Setiap tab = sesi chat independen (ag + id sesi sendiri). Tab 0 memakai
	// ag utama (termasuk riwayat yang di-restore). `pending` menyimpan arahan
	// operator yang SEDANG dieksekusi — sehingga Ctrl+C di tengah misi tetap
	// meninggalkan jejak arahan pada sesi.
	type tabState struct {
		ag      *agent.Agent
		sesiID  string
		started time.Time
		pending []llm.Message
	}
	tabs := []*tabState{{ag: ag, sesiID: sesiID, started: sesiMulai}} // sinkron dengan index tab TUI
	curTab := 0
	saveTab := func(ts *tabState) {
		if ts == nil || ts.ag == nil {
			return
		}
		msgs := ts.ag.Snapshot()
		msgs = append(msgs, ts.pending...)
		if len(msgs) == 0 {
			return // tidak ada yang layak disimpan
		}
		saveSession(ts.sesiID, ts.started, msgs)
	}
	saveAllTabs := func() {
		for _, ts := range tabs {
			saveTab(ts)
		}
	}
	sesiSave := func() { saveTab(tabs[curTab]) }
	// printSave mencetak lokasi sesi tersimpan + cara membuka ulang — HANYA
	// setelah layar penuh ditutup (sink TUI dilepas dulu agar pesan terlihat
	// di layar biasa). Terlindungi agar tidak tercetak dobel bila Ctrl+C
	// memicu dua jalur sekaligus (editor raw + SIGINT).
	var savedMsgOnce sync.Once
	printSave := func(tabID string) {
		savedMsgOnce.Do(func() {
			ui.SetSink(nil)
			ui.Info("")
			ui.Info("Sesi posko ditutup — sesi tersimpan:")
			ui.Info("  " + sessionDir() + "/sesi-" + tabID + ".json")
			ui.Info(ui.Dim("  Buka ulang: kartika --lanjut          (sesi terakhir)"))
			ui.Info(ui.Dim("             kartika --lanjut " + tabID + "  (sesi ini)"))
			ui.Info(ui.Dim("             kartika -s                  (daftar sesi)"))
		})
	}
	exitHook.Store(func() {
		saveAllTabs()
		ui.ExitAlt()
		printSave(tabs[curTab].sesiID)
	})
	ui.SetInterruptHandler(func() {
		saveAllTabs()
		ui.ExitAlt()
		printSave(tabs[curTab].sesiID)
		cleanup()
	})

	// Toggle alur berpikir model (reasoning): tersembunyi (latar belakang)
	// secara default; Ctrl+B atau /pikir memunculkannya ke depan.
	reasonOn := &atomic.Bool{}
	toggleReason := func() {
		if reasonOn.Load() {
			reasonOn.Store(false)
			ui.Info("")
			ui.Info(ui.Dim("  ▣ pemikiran model DISEMBUNYIKAN — ctrl+b / /pikir untuk menampilkan"))
		} else {
			reasonOn.Store(true)
			ui.Info("")
			ui.Info(ui.Dim("  ▣ pemikiran model DITAMPILKAN — ctrl+b / /pikir untuk menyembunyikan"))
		}
	}
	ui.SetCtrlBHandler(toggleReason)

	if ui.TUIAvailable() {
		// ---------- LAYAR PENUH (alternate screen, gaya opencode) ----------
		ui.SetSpinnerInert(true) // status bar yang menunjukkan "berpikir"
		tui := ui.NewTUI()
		ui.SetSink(tui.Append)
		ui.EnterAlt()
		ui.OnResize(tui.Resize)
		statusIdle := func(sesiID string) string {
			return " sesi " + sesiID + " │ ctrl+b pemikiran model · ctrl+t tab baru · alt+1..9 pindah tab │ /bantuan │ /keluar"
		}
		// Hook tab: TUI memanggil ini saat tab baru dibuat (ctrl+t) atau
		// pandang berpindah (alt+1..9) — main menukar ag + sesi aktif.
		tui.SetTabHook(func(idx int) {
			if curTab >= 0 && curTab < len(tabs) {
				saveTab(tabs[curTab]) // simpan sesi tab yang ditinggalkan
			}
			curTab = idx
			var ts *tabState
			if idx >= 0 && idx < len(tabs) {
				ts = tabs[idx]
			} else {
				ts = &tabState{ag: agent.New(cfg, engine, reg), sesiID: newSessionID(), started: time.Now()}
				tabs = append(tabs, ts)
			}
			sesiID = ts.sesiID
			tui.SetTabTitle(ts.sesiID)
			tui.SetStatus(statusIdle(ts.sesiID))
		})
		// Hook tutup tab (klik ikon × di header): sesi tab yang ditutup
		// disimpan, agen dibatalkan bila engine masih berjalan di sana,
		// dan slice utama disinkronkan dengan index TUI.
		tui.SetTabCloseHook(func(idx int) {
			if idx < 0 || idx >= len(tabs) {
				return
			}
			saveTab(tabs[idx])
			tabs[idx].ag.Interrupt() // no-op bila engine sedang diam
			tabs = append(tabs[:idx], tabs[idx+1:]...)
			if curTab >= len(tabs) {
				curTab = len(tabs) - 1
			}
		})
		tui.SetTabTitle(sesiID)
		tui.Append(ui.Bold(ui.Cyan("[KARTIKA-AGENT // TACTICAL CORE v" + version + "]")))
		tui.Append(ui.Dim("MODE POSKO LAYAR PENUH — ketik / untuk menu perintah · ctrl+b pemikiran model · ctrl+t tab baru · /keluar tutup"))
		if resumeNotice != "" {
			tui.Append(ui.Dim("  " + resumeNotice))
		}
		if up := updateBanner(); up != "" {
			tui.Append(up)
		}
		tui.SetStatus(statusIdle(sesiID))
		// Picker interaktif (anak panah warna + mouse) untuk /model,
		// /api-url, dan wizard konfigurasi — dipasok ReadPickerTUI.
		pickerTUI := func(title string, items []ui.PickerItem) (string, bool) {
			idx, ok := ui.ReadPickerTUI(title, items)
			if !ok {
				return "", false
			}
			return items[idx].Label, true
		}
		missions := 0
		for {
			line, err := ui.ReadInputTUI(tui, "CHAT ❯")
			if err != nil {
				break // EOF
			}
			line = strings.TrimSpace(line)
			// Echo pesan operator ke transkrip (gaya opencode) — jejak
			// apa yang diketik tertinggal di atas jawaban.
			if line != "" {
				tui.Append(ui.Bold(ui.Cyan("❯ ")) + ui.Bold(line))
			}
			switch strings.ToLower(line) {
			case "":
				continue
			case "/keluar", "keluar", "/exit", "exit", "/quit", "quit":
				saveAllTabs()
				ui.ExitAlt()
				printSave(tabs[curTab].sesiID)
				return
			case "/bantuan", "bantuan", "/help", "help", "?":
				tui.Append(ui.Bold(ui.Cyan("MODE POSKO — PERINTAH")))
				for _, c := range ui.SlashCommands {
					tui.Append("  " + c.Cmd + " : " + c.Desc)
				}
				tui.Append(ui.Dim("  ctrl+b : memunculkan pemikiran model di latar belakang ke depan — tekan lagi untuk menyembunyikan."))
				tui.Append(ui.Dim("  ctrl+t : tab sesi baru — alt+1..9 pindah tab."))
				tui.Append(ui.Dim("  Sesi otomatis tersimpan — `kartika --lanjut [id]` membuka ulang; `kartika -s` daftar sesi."))
				continue
			case "/status", "status":
				printStatusDetail(cfg, reg, mcpClients, missions, builtinCount)
				continue
			case "/modul", "modul":
				printModules(reg)
				continue
			case "/mcp", "mcp":
				printMCPStatus(cfg, mcpClients)
				continue
			case "/skill", "skill":
				if f := strings.Fields(line); len(f) > 1 {
					printSkillDoc(strings.Join(f[1:], " "))
				} else {
					printSkillList()
				}
				continue
			case "/pikir", "/reasoning":
				toggleReason()
				continue
			case "/bersih", "/clear":
				tui.Clear()
				tui.Append(ui.Bold(ui.Cyan("[KARTIKA-AGENT // TACTICAL CORE v" + version + "]")))
				continue
			case "/baru", "/reset":
				ts := tabs[curTab]
				ts.ag.ResetHistory()
				ts.sesiID = newSessionID()
				ts.started = time.Now()
				sesiID = ts.sesiID
				tui.SetTabTitle(ts.sesiID)
				ui.Info("  Memori percakapan dikosongkan — sesi baru siap.")
				tui.SetStatus(statusIdle(sesiID))
				continue
			case "/konfigurasi-api-key", "konfigurasi-api-key", "/konfigurasi", "konfigurasi", "/config", "config", "/masukan-api-key", "masukan-api-key", "/api-key", "api-key", "/apikey", "apikey":
				handleKonfigurasiAPIKey(cfg, engine, func(p string) (string, error) {
					return ui.ReadInputTUI(tui, p)
				}, tui.Append, pickerTUI)
				tui.SetStatus(statusIdle(sesiID))
				continue
			case "/konfigurasi-model-lokal", "konfigurasi-model-lokal", "/model-lokal", "model-lokal", "/lokal", "lokal":
				handleKonfigurasiModelLokal(cfg, engine, func(p string) (string, error) {
					return ui.ReadInputTUI(tui, p)
				}, tui.Append, pickerTUI)
				tui.SetStatus(statusIdle(sesiID))
				continue
			case "/pilih model", "pilih model", "/pilih-model", "pilih-model", "/model", "model":
				handlePilihModel("", cfg, engine, func(p string) (string, error) {
					return ui.ReadInputTUI(tui, p)
				}, tui.Append, pickerTUI)
				tui.SetStatus(statusIdle(sesiID))
				continue
			case "/api-url", "api-url", "/url", "url":
				handleAPIURL("", cfg, engine, func(p string) (string, error) {
					return ui.ReadInputTUI(tui, p)
				}, tui.Append, pickerTUI)
				tui.SetStatus(statusIdle(sesiID))
				continue
			case "/update", "update", "/perbarui", "perbarui", "/upgrade":
				tui.Clear()
				tui.Append(ui.Bold(ui.Cyan("[KARTIKA-AGENT // TACTICAL CORE v" + version + "]")))
				handleUpdate("", func(p string) (string, error) {
					return ui.ReadInputTUI(tui, p)
				}, tui.Append, pickerTUI, workDir)
				tui.SetStatus(statusIdle(sesiID))
				continue
			}
			if strings.HasPrefix(line, "/") {
				// Perintah berargumen: /skill <nama>
				low := strings.ToLower(line)
				if strings.HasPrefix(low, "/skill ") || strings.HasPrefix(low, "skill ") {
					if f := strings.Fields(line); len(f) > 1 {
						printSkillDoc(strings.Join(f[1:], " "))
					}
					continue
				}
				if strings.HasPrefix(low, "/konfigurasi-api-key") || strings.HasPrefix(low, "/masukan-api-key") || strings.HasPrefix(low, "/konfigurasi") {
					handleKonfigurasiAPIKey(cfg, engine, func(p string) (string, error) {
						return ui.ReadInputTUI(tui, p)
					}, tui.Append, pickerTUI)
					tui.SetStatus(statusIdle(sesiID))
					continue
				}
				if strings.HasPrefix(low, "/konfigurasi-model-lokal") || strings.HasPrefix(low, "/model-lokal") || strings.HasPrefix(low, "/lokal") {
					handleKonfigurasiModelLokal(cfg, engine, func(p string) (string, error) {
						return ui.ReadInputTUI(tui, p)
					}, tui.Append, pickerTUI)
					tui.SetStatus(statusIdle(sesiID))
					continue
				}
				if strings.HasPrefix(low, "/pilih model ") || strings.HasPrefix(low, "/pilih-model ") || strings.HasPrefix(low, "/model ") {
					f := strings.Fields(line)
					arg := ""
					if strings.HasPrefix(low, "/pilih model ") && len(f) > 2 {
						arg = strings.Join(f[2:], " ")
					} else if len(f) > 1 {
						arg = strings.Join(f[1:], " ")
					}
					handlePilihModel(arg, cfg, engine, func(p string) (string, error) {
						return ui.ReadInputTUI(tui, p)
					}, tui.Append, pickerTUI)
					tui.SetStatus(statusIdle(sesiID))
					continue
				}
				if strings.HasPrefix(low, "/api-url ") || strings.HasPrefix(low, "/url ") {
					f := strings.Fields(line)
					arg := ""
					if len(f) > 1 {
						arg = strings.Join(f[1:], " ")
					}
					handleAPIURL(arg, cfg, engine, func(p string) (string, error) {
						return ui.ReadInputTUI(tui, p)
					}, tui.Append, pickerTUI)
					tui.SetStatus(statusIdle(sesiID))
					continue
				}
				ui.Warn("Perintah tidak dikenali: " + line + " — ketik /bantuan untuk daftar perintah.")
				continue
			}
			missions++
			ts := tabs[curTab]
			tabAg, tabSesi := ts.ag, ts.sesiID
			ts.pending = []llm.Message{{Role: "user", Content: line}} // jejak misi berjalan
			tabTab := ts
			// Input live selama engine bekerja: operator bisa mengetik (Enter
			// = antri ke model), paste tanpa eksekusi, dan Ctrl+C menghentikan
			// GILIRAN chat — bukan keluar program.
			ui.SetLiveInputHandlers(
				func(s string) { tui.SetLiveInput(s) },
				func(text string) {
					tui.Append(ui.Bold(ui.Cyan("❯ ")) + ui.Bold(text) + ui.Dim("  ⟵ diantrikan ke model"))
					tabTab.pending = append(tabTab.pending, llm.Message{Role: "user", Content: text})
					tabAg.Inject(text)
				},
				func() { tabAg.Interrupt() },
			)
			var reasonBuf strings.Builder
			reasonOpen := false
			showReason := func(s string) {
				if !reasonOn.Load() {
					return
				}
				if !reasonOpen {
					tui.Append(ui.Dim("── BERPIKIR (ctrl+b menyembunyikan) ──"))
					reasonOpen = true
				}
				reasonBuf.WriteString(s)
				txt := reasonBuf.String()
				if rn := []rune(txt); len(rn) > 800 {
					txt = string(rn[len(rn)-800:])
				}
				tui.SetLive("▌ "+txt, true)
			}
			flushReason := func() {
				if reasonBuf.Len() > 0 || reasonOpen {
					tui.CommitLive()
					if reasonOpen {
						tui.Append(ui.Dim("── (akhir berpikir) ──"))
					}
					reasonBuf.Reset()
					reasonOpen = false
				}
			}
			tui.SetThinking(true, "engine berpikir")
			tap := ui.NewKeyTap()
			warnaiDelta := ui.NewDeltaColorizer()
			err = tabAg.RunChat(line, agent.ChatUI{
				OnDelta: func(d string) {
					flushReason()
					tui.Append(warnaiDelta(d))
				},
				OnReasoning: func(r string) bool {
					if !reasonOn.Load() {
						return false
					}
					showReason(r)
					return true
				},
				OnDeliberate: showReason,
				OnHUD:        flushReason,
			})
			tap.Stop()
			flushReason()
			tui.SetLiveInput("")
			ts.pending = nil
			tui.SetThinking(false, "")
			tui.SetStatus(statusIdle(tabSesi))
			if err != nil {
				if errors.Is(err, agent.ErrAborted) {
					ui.Info("")
					ui.Info(ui.Yellow("⏹ Chat dihentikan (ctrl+c) — kembali ke prompt. Sesinya tersimpan; ketik 'lanjutkan' untuk menuntaskan pekerjaan, atau /keluar untuk keluar."))
					ui.Info("")
				} else {
					reportFailure(err, cfg, showConfig)
				}
			}
			saveTab(ts)
		}
		saveAllTabs()
		ui.ExitAlt()
		printSave(tabs[curTab].sesiID)
		return
	}

	// ---------- CLI POLOS (pipe/CI atau terminal tanpa dukungan TUI) ----------
	ui.PrintPoskoHelp()
	if up := updateBanner(); up != "" {
		ui.Info(up)
	}
	missions := 0
	for {
		line, err := ui.ReadInput("CHAT ❯")
		if err != nil {
			fmt.Println()
			break
		}
		line = strings.TrimSpace(line)
		if line != "" {
			fmt.Println(ui.Bold(ui.Cyan("❯ ")) + ui.Bold(line))
		}
		switch strings.ToLower(line) {
		case "":
			continue
		case "/keluar", "keluar", "/exit", "exit", "/quit", "quit":
			sesiSave()
			printSave(sesiID)
			return
		case "/bantuan", "bantuan", "/help", "help", "?":
			ui.PrintPoskoHelp()
			continue
		case "/status", "status":
			printStatusDetail(cfg, reg, mcpClients, missions, builtinCount)
			continue
		case "/modul", "modul":
			printModules(reg)
			continue
		case "/mcp", "mcp":
			printMCPStatus(cfg, mcpClients)
			continue
		case "/skill", "skill":
			if f := strings.Fields(line); len(f) > 1 {
				printSkillDoc(strings.Join(f[1:], " "))
			} else {
				printSkillList()
			}
			continue
		case "/pikir", "/reasoning":
			toggleReason()
			continue
		case "/bersih", "/clear":
			fmt.Print("\x1b[2J\x1b[H")
			ui.PrintHeader(version)
			up := checkEngine(cfg)
			ui.PrintStatus(cfg.Model, builtinCount, len(mcpClients), up)
			if !up {
				showConfig("API atau model belum dikonfigurasi — engine tidak dapat dihubungi.")
			}
			continue
		case "/baru", "/reset":
			ag.ResetHistory()
			tabs[0].sesiID = newSessionID()
			tabs[0].started = time.Now()
			sesiID = tabs[0].sesiID
			ui.Info("  Memori percakapan dikosongkan — sesi baru siap.")
			continue
		case "/konfigurasi-api-key", "konfigurasi-api-key", "/konfigurasi", "konfigurasi", "/config", "config", "/masukan-api-key", "masukan-api-key", "/api-key", "api-key", "/apikey", "apikey":
			handleKonfigurasiAPIKey(cfg, engine, func(p string) (string, error) {
				return ui.ReadInput(p)
			}, func(s string) { fmt.Println(s) }, nil)
			continue
		case "/konfigurasi-model-lokal", "konfigurasi-model-lokal", "/model-lokal", "model-lokal", "/lokal", "lokal":
			handleKonfigurasiModelLokal(cfg, engine, func(p string) (string, error) {
				return ui.ReadInput(p)
			}, func(s string) { fmt.Println(s) }, nil)
			continue
		case "/pilih model", "pilih model", "/pilih-model", "pilih-model", "/model", "model":
			handlePilihModel("", cfg, engine, func(p string) (string, error) {
				return ui.ReadInput(p)
			}, func(s string) { fmt.Println(s) }, nil)
			continue
		case "/api-url", "api-url", "/url", "url":
			handleAPIURL("", cfg, engine, func(p string) (string, error) {
				return ui.ReadInput(p)
			}, func(s string) { fmt.Println(s) }, nil)
			continue
		case "/update", "update", "/perbarui", "perbarui", "/upgrade":
			fmt.Print("\x1b[2J\x1b[H")
			ui.PrintHeader(version)
			handleUpdate("", func(p string) (string, error) {
				return ui.ReadInput(p)
			}, func(s string) { fmt.Println(s) }, nil, workDir)
			continue
		}
		if strings.HasPrefix(line, "/") {
			// Perintah berargumen: /skill <nama>
			low := strings.ToLower(line)
			if strings.HasPrefix(low, "/skill ") || strings.HasPrefix(low, "skill ") {
				if f := strings.Fields(line); len(f) > 1 {
					printSkillDoc(strings.Join(f[1:], " "))
				}
				continue
			}
			if strings.HasPrefix(low, "/konfigurasi-api-key") || strings.HasPrefix(low, "/masukan-api-key") || strings.HasPrefix(low, "/konfigurasi") {
				handleKonfigurasiAPIKey(cfg, engine, func(p string) (string, error) {
					return ui.ReadInput(p)
				}, func(s string) { fmt.Println(s) }, nil)
				continue
			}
			if strings.HasPrefix(low, "/konfigurasi-model-lokal") || strings.HasPrefix(low, "/model-lokal") || strings.HasPrefix(low, "/lokal") {
				handleKonfigurasiModelLokal(cfg, engine, func(p string) (string, error) {
					return ui.ReadInput(p)
				}, func(s string) { fmt.Println(s) }, nil)
				continue
			}
			if strings.HasPrefix(low, "/pilih model ") || strings.HasPrefix(low, "/pilih-model ") || strings.HasPrefix(low, "/model ") {
				f := strings.Fields(line)
				arg := ""
				if strings.HasPrefix(low, "/pilih model ") && len(f) > 2 {
					arg = strings.Join(f[2:], " ")
				} else if len(f) > 1 {
					arg = strings.Join(f[1:], " ")
				}
				handlePilihModel(arg, cfg, engine, func(p string) (string, error) {
					return ui.ReadInput(p)
				}, func(s string) { fmt.Println(s) }, nil)
				continue
			}
			if strings.HasPrefix(low, "/api-url ") || strings.HasPrefix(low, "/url ") {
				f := strings.Fields(line)
				arg := ""
				if len(f) > 1 {
					arg = strings.Join(f[1:], " ")
				}
				handleAPIURL(arg, cfg, engine, func(p string) (string, error) {
					return ui.ReadInput(p)
				}, func(s string) { fmt.Println(s) }, nil)
				continue
			}
			ui.Warn("Perintah tidak dikenali: " + line + " — ketik /bantuan untuk daftar perintah.")
			continue
		}
		missions++
		fmt.Println()
		ui.TurnRule("KARTIKA")
		tabs[0].pending = []llm.Message{{Role: "user", Content: line}} // jejak misi berjalan
		cliTab := tabs[0]

		// Input live selama engine bekerja (CLI polos): Enter = antri ke
		// model, Ctrl+C menghentikan GILIRAN chat — bukan keluar program.
		ui.SetLiveInputHandlers(
			nil, // tanpa TUI: tidak ada render live
			func(text string) {
				fmt.Println(ui.Bold(ui.Cyan("❯ ")) + ui.Bold(text) + ui.Dim("  ⟵ diantrikan ke model"))
				cliTab.pending = append(cliTab.pending, llm.Message{Role: "user", Content: text})
				ag.Inject(text)
			},
			func() { ag.Interrupt() },
		)

		// Tangkap Ctrl+B selama engine bekerja (keytap; no-op di pipe/CI).
		tap := ui.NewKeyTap()
		lastReason := false
		closeReason := func() {
			if lastReason {
				lastReason = false
				fmt.Println()
				fmt.Println(ui.Dim("── (akhir berpikir) " + strings.Repeat("─", 42)))
			}
		}
		showReason := func(s string) {
			if !reasonOn.Load() {
				return
			}
			if !lastReason {
				fmt.Println()
				fmt.Println(ui.Dim("── BERPIKIR (ctrl+b menyembunyikan) " + strings.Repeat("─", 25)))
				lastReason = true
			}
			fmt.Print(ui.Dim(s))
		}
		warnaiDelta := ui.NewDeltaColorizer()
		err = ag.RunChat(line, agent.ChatUI{
			OnDelta: func(d string) {
				closeReason()
				fmt.Print(warnaiDelta(d))
			},
			OnReasoning: func(r string) bool {
				if !reasonOn.Load() {
					return false
				}
				showReason(r)
				return true
			},
			OnDeliberate: showReason,
			OnHUD:        closeReason,
		})
		tap.Stop()
		closeReason()
		tabs[0].pending = nil
		if err != nil {
			if errors.Is(err, agent.ErrAborted) {
				fmt.Println()
				fmt.Println(ui.Yellow("⏹ Chat dihentikan (ctrl+c) — kembali ke prompt. Sesinya tersimpan; ketik 'lanjutkan' untuk menuntaskan pekerjaan."))
				fmt.Println()
			} else {
				reportFailure(err, cfg, showConfig)
			}
		}
		sesiSave()
	}
	sesiSave()
	printSave(sesiID)
}

// checkEngine melakukan ping cepat ke endpoint engine (GET /models).
// Endpoint yang merespons (walau 401/403) dianggap terhubung — kegagalan
// kunci API dikeluhkan saat chat dengan pesan yang spesifik.
func checkEngine(cfg *config.Config) bool {
	cl := &http.Client{Timeout: 3 * time.Second}
	req, err := http.NewRequest("GET", strings.TrimRight(cfg.APIURL, "/")+"/models", nil)
	if err != nil {
		return false
	}
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	resp, err := cl.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 500
}

// fetchAvailableModels mengambil daftar model yang tersedia dari engine AI.
func fetchAvailableModels(cfg *config.Config) ([]string, error) {
	baseURL := strings.TrimRight(cfg.APIURL, "/")
	cl := &http.Client{Timeout: 3 * time.Second}

	var found []string
	seen := make(map[string]bool)

	// 1. Coba endpoint OpenAI /models (didukung Ollama v1, vLLM, Groq, OpenAI, dll.)
	req, err := http.NewRequest("GET", baseURL+"/models", nil)
	if err == nil {
		if cfg.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
		}
		resp, err := cl.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				var data struct {
					Data []struct {
						ID string `json:"id"`
					} `json:"data"`
				}
				if json.NewDecoder(resp.Body).Decode(&data) == nil {
					for _, m := range data.Data {
						id := strings.TrimSpace(m.ID)
						if id != "" && !seen[id] {
							seen[id] = true
							found = append(found, id)
						}
					}
				}
			}
		}
	}

	// 2. Fallback untuk Ollama native endpoint: /api/tags
	if len(found) == 0 {
		host := baseURL
		if idx := strings.Index(baseURL, "/v1"); idx != -1 {
			host = baseURL[:idx]
		}
		req2, err := http.NewRequest("GET", host+"/api/tags", nil)
		if err == nil {
			resp2, err := cl.Do(req2)
			if err == nil {
				defer resp2.Body.Close()
				if resp2.StatusCode == http.StatusOK {
					var data struct {
						Models []struct {
							Name string `json:"name"`
						} `json:"models"`
					}
					if json.NewDecoder(resp2.Body).Decode(&data) == nil {
						for _, m := range data.Models {
							name := strings.TrimSpace(m.Name)
							if name != "" && !seen[name] {
								seen[name] = true
								found = append(found, name)
							}
						}
					}
				}
			}
		}
	}

	if len(found) == 0 {
		return nil, errors.New("tidak ada model yang terdeteksi")
	}
	sort.Strings(found)
	return found, nil
}

// pickFn adalah fungsi picker interaktif (mode TUI: ReadPickerTUI dengan
// anak panah warna + mouse); nil pada mode CLI polos → fallback bernomor.
type pickFn func(title string, items []ui.PickerItem) (string, bool)

// chooseAtauManual menampilkan pilihan lewat picker interaktif (TUI) atau
// daftar bernomor (CLI polos). Pilihan terakhir selalu "✎ ketik manual".
// Mengembalikan (nilai, true) bila ada pilihan; ("", false) bila batal.
func chooseAtauManual(pick pickFn, readInput func(string) (string, error), appendMsg func(string),
	title string, items []ui.PickerItem, manualLabel, manualPrompt string) (string, bool) {

	manual := ui.PickerItem{Label: "✎ " + manualLabel, Desc: "ketik sendiri"}
	if pick != nil {
		all := append(append([]ui.PickerItem(nil), items...), manual)
		val, ok := pick(title, all)
		if !ok {
			return "", false
		}
		if val == manual.Label { // item manual dipilih
			in, err := readInput(manualPrompt)
			if err != nil {
				return "", false
			}
			in = strings.TrimSpace(in)
			if in == "" || strings.EqualFold(in, "/batal") || strings.EqualFold(in, "batal") {
				return "", false
			}
			return in, true
		}
		return val, true
	}

	// Mode CLI polos: daftar bernomor + ketik manual.
	appendMsg(ui.Bold(title))
	for i, it := range items {
		appendMsg(fmt.Sprintf("    [%d] %-14s %s", i+1, it.Label, ui.Dim(it.Desc)))
	}
	appendMsg(fmt.Sprintf("  Pilih nomor [1-%d], atau ketik %s (atau /batal):", len(items), manualLabel))
	in, err := readInput("PILIH ❯")
	if err != nil {
		return "", false
	}
	in = strings.TrimSpace(in)
	if in == "" || strings.EqualFold(in, "/batal") || strings.EqualFold(in, "batal") {
		return "", false
	}
	if num, perr := strconv.Atoi(in); perr == nil && num >= 1 && num <= len(items) {
		return items[num-1].Label, true
	}
	return in, true // teks bebas = nilai manual
}

func handleKonfigurasiAPIKey(cfg *config.Config, engine *llm.Client, readInput func(string) (string, error), appendMsg func(string), pick pickFn) {
	appendMsg(ui.Bold(ui.Cyan("┌─ KONFIGURASI ENGINE AI — 3 LANGKAH")))
	appendMsg(ui.Dim("  Alur: ❶ provider & URL → ❷ API key → ❸ model. Pilih lewat picker"))
	appendMsg(ui.Dim("  (↑↓ atau mouse) atau ketik manual; /batal menghentikan kapan saja."))
	appendMsg("")

	// ❶ PROVIDER & URL — picker berwarna (TUI) / daftar bernomor (CLI).
	presetItems := make([]ui.PickerItem, 0, len(config.ProviderPresets)+1)
	for _, p := range config.ProviderPresets {
		desc := p.URL
		if strings.TrimRight(cfg.APIURL, "/") == strings.TrimRight(p.URL, "/") {
			desc = p.URL + "  " + ui.Green("← aktif")
		}
		presetItems = append(presetItems, ui.PickerItem{Label: p.Name, Desc: desc})
	}
	presetItems = append(presetItems, ui.PickerItem{Label: "⏭ Lewati", Desc: "pakai URL aktif: " + cfg.APIURL})
	val, ok := chooseAtauManual(pick, readInput, appendMsg,
		"❶ PILIH PROVIDER / URL ENGINE", presetItems,
		"URL lengkap lain (mis. http://192.168.1.5:11434/v1)", "URL ❯")
	if !ok {
		appendMsg(ui.Dim("  Konfigurasi dibatalkan — nilai lama tetap dipakai."))
		return
	}
	newURL := cfg.APIURL
	if val != cfg.APIURL {
		if p := cariPreset(val); p.Name != "" {
			newURL = p.URL
			cfg.Provider = p.Name
		} else {
			newURL = val
			if !strings.HasPrefix(newURL, "http://") && !strings.HasPrefix(newURL, "https://") {
				newURL = "http://" + newURL
			}
			cfg.Provider = ""
		}
	}
	appendMsg("  ❶ URL  → " + ui.Cyan(newURL))
	appendMsg("")

	// ❷ API KEY — teks biasa; kunci selalu disamarkan saat ditampilkan.
	appendMsg(ui.Bold("❷ API KEY (BEARER TOKEN)"))
	masked := cfg.APIKey
	if masked == "" {
		masked = "(kosong)"
	} else if len(masked) > 8 {
		masked = masked[:4] + "..." + masked[len(masked)-4:]
	}
	appendMsg("  Kunci aktif : " + ui.Dim(masked))
	if cfg.Provider == "ollama" || cfg.Provider == "vllm" {
		appendMsg(ui.Dim("  Engine LOKAL biasanya TANPA kunci — tekan Enter untuk melewati."))
	} else {
		appendMsg(ui.Dim("  Tempel kunci dari provider (Groq: gsk_… · OpenAI: sk-… · OpenRouter: sk-or-v1-… · NVIDIA: nvapi-…)."))
	}
	appendMsg("  Ketik / b paste kunci (Enter = tetap pakai yang lama):")
	inKey, err := readInput("API KEY ❯")
	if err != nil {
		return
	}
	inKey = strings.TrimSpace(inKey)
	if strings.EqualFold(inKey, "/batal") || strings.EqualFold(inKey, "batal") {
		appendMsg(ui.Dim("  Konfigurasi dibatalkan — nilai lama tetap dipakai."))
		return
	}
	newKey := cfg.APIKey
	if inKey != "" {
		newKey = strings.Trim(inKey, `"'`)
	}
	newMasked := newKey
	if len(newMasked) > 8 {
		newMasked = newMasked[:4] + "..." + newMasked[len(newMasked)-4:]
	}
	appendMsg("  ❷ KUNCI → " + ui.Dim(newMasked))
	appendMsg("")

	// ❸ MODEL — daftar model ditarik dari engine lalu dipilih via picker.
	appendMsg(ui.Bold("❸ MODEL AI"))
	appendMsg("  Menghubungi engine " + ui.Cyan(newURL) + ui.Dim(" …"))
	tempCfg := &config.Config{APIURL: newURL, APIKey: newKey}
	models, _ := fetchAvailableModels(tempCfg)
	newModel := cfg.Model
	if len(models) > 0 {
		modelItems := make([]ui.PickerItem, 0, len(models))
		for _, m := range models {
			desc := ""
			if m == cfg.Model {
				desc = ui.Green("← aktif")
			}
			modelItems = append(modelItems, ui.PickerItem{Label: m, Desc: desc})
		}
		val, ok := chooseAtauManual(pick, readInput, appendMsg,
			"❸ PILIH MODEL ("+fmt.Sprint(len(models))+" terdeteksi)", modelItems,
			"nama model lain (mis. llama3.3:70b)", "MODEL ❯")
		if !ok {
			appendMsg(ui.Dim("  Konfigurasi dibatalkan — nilai lama tetap dipakai."))
			return
		}
		newModel = strings.Trim(val, `"'`)
	} else {
		appendMsg(ui.Yellow("! Daftar model tidak dapat ditarik (engine mati / endpoint tidak standar)."))
		appendMsg("  Ketik nama model (Enter = tetap " + cfg.Model + "):")
		inModel, rerr := readInput("MODEL ❯")
		if rerr != nil {
			return
		}
		inModel = strings.TrimSpace(inModel)
		if strings.EqualFold(inModel, "/batal") || strings.EqualFold(inModel, "batal") {
			appendMsg(ui.Dim("  Konfigurasi dibatalkan — nilai lama tetap dipakai."))
			return
		}
		if inModel != "" {
			newModel = strings.Trim(inModel, `"'`)
		}
	}
	newURL = strings.TrimRight(newURL, "/")
	if len(models) > 0 {
		found := false
		for _, m := range models {
			if strings.EqualFold(m, newModel) {
				newModel = m
				found = true
				break
			}
		}
		if !found {
			appendMsg(ui.Yellow("! Model '" + newModel + "' tidak terdeteksi di daftar engine — tetap disimpan."))
		}
	}
	appendMsg("  ❸ MODEL → " + ui.Cyan(newModel))
	appendMsg("")

	cfg.APIURL = newURL
	cfg.APIKey = newKey
	cfg.Model = newModel
	if engine != nil {
		engine.URL = newURL
		engine.APIKey = newKey
		engine.Model = newModel
	}
	_ = config.SaveEnvVar("KARTIKA_API_URL", newURL)
	_ = config.SaveEnvVar("KARTIKA_API_KEY", newKey)
	_ = config.SaveEnvVar("KARTIKA_MODEL", newModel)
	ui.SetLocalMode(cfg.IsLocal())

	ok2 := checkEngine(cfg)
	appendMsg(ui.Green("✓ Konfigurasi tersimpan ke ~/.config/kartika/.env"))
	appendMsg("  URL   : " + ui.Cyan(newURL))
	appendMsg("  Key   : " + ui.Dim(newMasked))
	appendMsg("  Model : " + ui.Cyan(newModel))
	if ok2 {
		appendMsg(ui.Green("✓ Engine: " + ui.Bold("ONLINE") + " — siap digunakan."))
	} else {
		appendMsg(ui.Yellow("! Engine: BELUM TERHUBUNG — periksa URL, kunci, dan nama model."))
	}
}

// handleKonfigurasiModelLokal — wizard khusus engine LOKAL: pilih server
// (Ollama / vLLM / LM Studio / llama.cpp / URL lain), tarik daftar model,
// pilih model, simpan TANPA API key, lalu tes koneksi.
func handleKonfigurasiModelLokal(cfg *config.Config, engine *llm.Client, readInput func(string) (string, error), appendMsg func(string), pick pickFn) {
	appendMsg(ui.Bold(ui.Cyan("┌─ KONFIGURASI MODEL LOKAL")))
	appendMsg(ui.Dim("  Engine lokal berjalan di laptop Anda sendiri — TANPA API key,"))
	appendMsg(ui.Dim("  cocok untuk udara-tertutup (air-gapped). /batal menghentikan kapan saja."))
	appendMsg("")
	lokItems := []ui.PickerItem{
		{Label: "http://localhost:11434/v1", Desc: "Ollama   (port 11434)"},
		{Label: "http://localhost:8000/v1", Desc: "vLLM     (port 8000)"},
		{Label: "http://localhost:1234/v1", Desc: "LM Studio (port 1234)"},
		{Label: "http://localhost:8080/v1", Desc: "llama.cpp server (port 8080)"},
	}
	val, ok := chooseAtauManual(pick, readInput, appendMsg,
		"PILIH SERVER LOKAL", lokItems,
		"URL server lain (mis. http://192.168.1.5:11434/v1)", "URL ❯")
	if !ok {
		appendMsg(ui.Dim("  Konfigurasi model lokal dibatalkan."))
		return
	}
	newURL := strings.TrimRight(val, "/")
	appendMsg("  Server → " + ui.Cyan(newURL))
	cfg.Provider = "lokal"

	// Tarik daftar model dari server lokal.
	appendMsg("  Mengambil daftar model …")
	tempCfg := &config.Config{APIURL: newURL}
	models, _ := fetchAvailableModels(tempCfg)
	newModel := ""
	if len(models) > 0 {
		items := make([]ui.PickerItem, 0, len(models))
		for _, m := range models {
			desc := ""
			if m == cfg.Model {
				desc = ui.Green("← aktif")
			}
			items = append(items, ui.PickerItem{Label: m, Desc: desc})
		}
		val, ok := chooseAtauManual(pick, readInput, appendMsg,
			"PILIH MODEL LOKAL ("+fmt.Sprint(len(models))+" terdeteksi)", items,
			"nama model manual", "MODEL ❯")
		if !ok {
			appendMsg(ui.Dim("  Konfigurasi model lokal dibatalkan."))
			return
		}
		newModel = strings.Trim(val, `"'`)
	} else {
		appendMsg(ui.Yellow("! Server tidak menjawab atau belum menjalankan model — pastikan"))
		appendMsg(ui.Dim("  Ollama/vLLM/LM Studio sedang berjalan, lalu ketik nama modelnya."))
		appendMsg("  Ketik nama model (atau /batal):")
		inModel, rerr := readInput("MODEL ❯")
		if rerr != nil {
			return
		}
		inModel = strings.TrimSpace(inModel)
		if inModel == "" || strings.EqualFold(inModel, "/batal") || strings.EqualFold(inModel, "batal") {
			appendMsg(ui.Dim("  Konfigurasi model lokal dibatalkan."))
			return
		}
		newModel = strings.Trim(inModel, `"'`)
	}
	if newModel == "" {
		appendMsg(ui.Yellow("  Nama model tidak boleh kosong."))
		return
	}

	cfg.APIURL = newURL
	cfg.APIKey = "" // lokal: tanpa kunci
	cfg.Model = newModel
	if engine != nil {
		engine.URL = newURL
		engine.APIKey = ""
		engine.Model = newModel
	}
	_ = config.SaveEnvVar("KARTIKA_API_URL", newURL)
	_ = config.SaveEnvVar("KARTIKA_API_KEY", "")
	_ = config.SaveEnvVar("KARTIKA_MODEL", newModel)
	ui.SetLocalMode(cfg.IsLocal())

	ok3 := checkEngine(cfg)
	appendMsg(ui.Green("✓ Konfigurasi lokal tersimpan ke ~/.config/kartika/.env"))
	appendMsg("  Server : " + ui.Cyan(newURL))
	appendMsg("  Model  : " + ui.Cyan(newModel))
	appendMsg("  API key: " + ui.Dim("(kosong — engine lokal)"))
	if ok3 {
		appendMsg(ui.Green("✓ Engine lokal: " + ui.Bold("ONLINE") + " — siap digunakan tanpa internet."))
	} else {
		appendMsg(ui.Yellow("! Engine lokal belum terhubung — cek apakah server berjalan (mis. 'ollama serve')."))
	}
}

// cariPreset mencari preset provider dari nama atau URL.
func cariPreset(v string) config.ProviderURL {
	for _, p := range config.ProviderPresets {
		if strings.EqualFold(v, p.Name) || strings.TrimRight(v, "/") == strings.TrimRight(p.URL, "/") {
			return p
		}
	}
	return config.ProviderURL{}
}

func handlePilihModel(arg string, cfg *config.Config, engine *llm.Client, readInput func(string) (string, error), appendMsg func(string), pick pickFn) {
	chosen := strings.TrimSpace(arg)
	var models []string
	if chosen == "" {
		appendMsg(ui.Bold(ui.Cyan("PILIH MODEL AI (KARTIKA_MODEL)")))
		appendMsg(ui.Dim("  Model saat ini: " + cfg.Model))
		appendMsg(ui.Dim("  Menghubungi engine di " + cfg.APIURL + "..."))

		models, _ = fetchAvailableModels(cfg)
		if len(models) > 0 {
			items := make([]ui.PickerItem, 0, len(models))
			for _, m := range models {
				desc := ""
				if m == cfg.Model {
					desc = ui.Green("← aktif")
				}
				items = append(items, ui.PickerItem{Label: m, Desc: desc})
			}
			val, ok := chooseAtauManual(pick, readInput, appendMsg,
				"PILIH MODEL ("+fmt.Sprint(len(models))+" terdeteksi)", items,
				"nama model lain (mis. llama3.3:70b)", "MODEL ❯")
			if !ok {
				appendMsg(ui.Dim("  Pemilihan model dibatalkan."))
				return
			}
			chosen = val
		} else {
			appendMsg(ui.Yellow("  Daftar model tidak dapat diambil dari engine (engine mati / endpoint tidak standar)."))
			appendMsg("  Ketik nama model yang ingin digunakan (atau /batal):")
			in, rerr := readInput("MODEL ❯")
			if rerr != nil {
				return
			}
			in = strings.TrimSpace(in)
			if in == "" || strings.ToLower(in) == "/batal" || strings.ToLower(in) == "batal" {
				appendMsg(ui.Dim("  Pemilihan model dibatalkan."))
				return
			}
			chosen = in
		}
	}

	chosen = strings.Trim(chosen, `"'`)
	if chosen == "" {
		appendMsg(ui.Yellow("  Nama model tidak boleh kosong."))
		return
	}

	if len(models) > 0 {
		found := false
		for _, m := range models {
			if strings.EqualFold(m, chosen) {
				chosen = m
				found = true
				break
			}
		}
		if !found {
			appendMsg(ui.Yellow("! PERINGATAN: Model '" + chosen + "' tidak terdaftar di engine."))
		}
	}

	cfg.Model = chosen
	if engine != nil {
		engine.Model = chosen
	}
	_ = config.SaveEnvVar("KARTIKA_MODEL", chosen)

	ok := checkEngine(cfg)
	appendMsg(ui.Green("✓ Model aktif diubah menjadi: " + chosen))
	appendMsg(ui.Dim("  Tersimpan di ~/.config/kartika/.env"))
	if ok {
		appendMsg(ui.Green("✓ Engine AI [ONLINE] dengan model " + chosen))
	} else {
		appendMsg(ui.Yellow("! Engine belum merespons model ini di " + cfg.APIURL + ". Pastikan model sudah siap di engine."))
	}
}

func handleAPIURL(arg string, cfg *config.Config, engine *llm.Client, readInput func(string) (string, error), appendMsg func(string), pick pickFn) {
	chosenURL := strings.TrimSpace(arg)
	if chosenURL == "" {
		items := make([]ui.PickerItem, 0, len(config.ProviderPresets))
		for _, p := range config.ProviderPresets {
			desc := p.URL
			if strings.TrimRight(cfg.APIURL, "/") == strings.TrimRight(p.URL, "/") {
				desc += "  " + ui.Green("← aktif")
			}
			items = append(items, ui.PickerItem{Label: p.Name, Desc: desc})
		}
		val, ok := chooseAtauManual(pick, readInput, appendMsg,
			"PILIH PROVIDER / URL ENGINE", items,
			"URL lengkap (mis. https://api.groq.com/openai/v1)", "API URL ❯")
		if !ok {
			appendMsg(ui.Dim("  Pengaturan API URL dibatalkan."))
			return
		}
		if p := cariPreset(val); p.Name != "" {
			chosenURL = p.URL
			cfg.Provider = p.Name
		} else {
			chosenURL = val
		}
	}

	chosenURL = strings.Trim(chosenURL, `"'`)
	if chosenURL == "" {
		appendMsg(ui.Yellow("  API URL tidak boleh kosong."))
		return
	}
	if !strings.HasPrefix(chosenURL, "http://") && !strings.HasPrefix(chosenURL, "https://") {
		chosenURL = "http://" + chosenURL
	}

	cfg.APIURL = chosenURL
	if engine != nil {
		engine.URL = chosenURL
	}
	_ = config.SaveEnvVar("KARTIKA_API_URL", chosenURL)
	ui.SetLocalMode(cfg.IsLocal())

	ok := checkEngine(cfg)
	appendMsg(ui.Green("✓ API URL berhasil diubah menjadi: " + chosenURL))
	appendMsg(ui.Dim("  Tersimpan di ~/.config/kartika/.env"))
	if ok {
		appendMsg(ui.Green("✓ Engine AI [ONLINE] — model: " + cfg.Model))
	} else {
		appendMsg(ui.Yellow("! Engine belum merespons di URL baru. Pastikan endpoint aktif dan API key sesuai."))
	}
}

// handleUpdate — perintah /update: cek rilis terbaru di repo GitHub,
// tampilkan perbandingan versi, lalu tawarkan tiga jalur pemasangan:
// pasang binary rilis (unduh + verifikasi SHA-256), build ulang dari
// source di mesin ini, atau sekadar buka halaman rilis.
func handleUpdate(arg string, readInput func(string) (string, error), appendMsg func(string), pick pickFn, srcDir string) {
	arg = strings.ToLower(strings.TrimSpace(arg))
	appendMsg(ui.Bold(ui.Cyan("┌─ PEMBARUAN KARTIKA-AGENT")))
	appendMsg(ui.Dim("  Repo     : " + update.Repo()))
	appendMsg(ui.Dim("  Platform : " + update.AssetName()))
	appendMsg(ui.Dim("  Terpasang: v" + version))
	appendMsg("")
	appendMsg("  Memeriksa rilis terbaru …")

	rel, err := update.Latest(8 * time.Second)
	if err != nil {
		appendMsg(ui.Red("✗ Gagal memeriksa rilis: " + err.Error()))
		appendMsg(ui.Dim("  Periksa koneksi internet, atau buka manual: " + update.PageURL()))
		return
	}
	appendMsg("  Terbaru  : " + ui.Cyan(rel.Tag))
	if rel.URL != "" {
		appendMsg(ui.Dim("  Halaman  : " + rel.URL))
	}
	if !update.Newer(version, rel.Tag) {
		appendMsg(ui.Green("✓ Sudah versi terbaru — tidak ada yang perlu dipasang."))
		if notes := update.NotesRingkas(rel.Notes, 8); notes != "" {
			appendMsg("")
			appendMsg(ui.Dim("  Catatan rilis:"))
			for _, l := range strings.Split(notes, "\n") {
				appendMsg(ui.Dim("    " + l))
			}
		}
		appendMsg("")
		return
	}
	appendMsg(ui.Yellow("↑ Tersedia pembaruan: v" + version + " → " + rel.Tag))
	if notes := update.NotesRingkas(rel.Notes, 10); notes != "" {
		appendMsg("")
		appendMsg(ui.Dim("  Catatan rilis:"))
		for _, l := range strings.Split(notes, "\n") {
			appendMsg(ui.Dim("    " + l))
		}
	}
	appendMsg("")

	asset, hasAsset := rel.FindAsset()
	opsi := make([]ui.PickerItem, 0, 3)
	if hasAsset {
		opsi = append(opsi, ui.PickerItem{
			Label: "⬇ Pasang binary rilis",
			Desc:  asset.Name + " · " + update.HumanSize(asset.Size),
		})
	} else {
		appendMsg(ui.Yellow("! Aset untuk platform ini tidak ada di rilis (" + update.AssetName() + ")."))
	}
	opsi = append(opsi, ui.PickerItem{Label: "🔨 Build ulang dari source", Desc: "butuh toolchain Go di mesin ini"})
	opsi = append(opsi, ui.PickerItem{Label: "🌐 Buka halaman rilis", Desc: update.PageURL()})

	var pilihan string
	if pick != nil {
		val, ok := pick("CARA MEMBARUI", opsi)
		if !ok {
			appendMsg(ui.Dim("  Pembaruan dibatalkan."))
			return
		}
		pilihan = val
	} else {
		appendMsg(ui.Bold("CARA MEMBARUI"))
		for i, it := range opsi {
			appendMsg(fmt.Sprintf("    [%d] %s %s", i+1, it.Label, ui.Dim(it.Desc)))
		}
		appendMsg(fmt.Sprintf("  Pilih nomor [1-%d] (atau /batal):", len(opsi)))
		in, rerr := readInput("PILIH ❯")
		if rerr != nil {
			return
		}
		in = strings.TrimSpace(in)
		n, cerr := strconv.Atoi(in)
		if cerr != nil || n < 1 || n > len(opsi) {
			appendMsg(ui.Dim("  Pembaruan dibatalkan."))
			return
		}
		pilihan = opsi[n-1].Label
	}

	switch {
	case strings.HasPrefix(pilihan, "⬇"):
		pasangBinaryRilis(rel, asset, appendMsg)
	case strings.HasPrefix(pilihan, "🔨"):
		buildUlangSource(srcDir, rel.Tag, appendMsg)
	default:
		appendMsg("")
		appendMsg("  Halaman rilis : " + update.PageURL())
		appendMsg("  Unduhan aset  : " + rel.URL)
		appendMsg(ui.Dim("  Pasang manual (Linux/macOS):"))
		appendMsg(ui.Dim("    curl -fL -o kartika " + asset.URL + " && chmod +x kartika && sudo mv kartika /usr/local/bin/"))
	}
	appendMsg("")
}

// pasangBinaryRilis mengunduh aset rilis, memverifikasi SHA-256 dari digest
// rilis, lalu mengganti binary yang sedang berjalan secara atomik.
func pasangBinaryRilis(rel *update.Release, asset update.Asset, appendMsg func(string)) {
	dst, err := update.CurrentExecutable()
	if err != nil {
		appendMsg(ui.Red("✗ Tidak dapat menentukan lokasi binary berjalan: " + err.Error()))
		return
	}
	appendMsg("")
	appendMsg("  Mengunduh " + asset.Name + " (" + update.HumanSize(asset.Size) + ") …")

	tmpDir, err := os.MkdirTemp("", "kartika-update-")
	if err != nil {
		appendMsg(ui.Red("✗ Gagal membuat direktori sementara: " + err.Error()))
		return
	}
	defer os.RemoveAll(tmpDir)
	tmpFile := filepath.Join(tmpDir, asset.Name)

	n, err := update.Download(asset.URL, tmpFile, 300*time.Second)
	if err != nil {
		appendMsg(ui.Red("✗ Unduhan gagal: " + err.Error()))
		return
	}
	appendMsg(ui.Green("✓ Terunduh: " + update.HumanSize(n)))

	checked, verr := update.VerifySHA256(tmpFile, asset.Digest)
	switch {
	case verr != nil:
		appendMsg(ui.Red("✗ Verifikasi SHA-256 GAGAL: " + verr.Error()))
		appendMsg(ui.Dim("  Berkas tidak dipasang — unduhan mungkin rusak atau diubah di jalan."))
		return
	case !checked:
		appendMsg(ui.Yellow("! Rilis tidak menyertakan checksum SHA-256 — verifikasi dilewati."))
	default:
		sum, _ := update.Sum256(tmpFile)
		appendMsg(ui.Green("✓ SHA-256 cocok: " + sum))
	}

	installedTo, err := update.Install(tmpFile, dst)
	if err != nil {
		appendMsg(ui.Red("✗ Gagal memasang: " + err.Error()))
		appendMsg(ui.Dim("  Saran: jalankan ulang dengan hak tulis, mis. 'sudo kartika' atau unduh manual dari " + rel.URL))
		return
	}
	appendMsg("")
	if filepath.Ext(installedTo) == ".new" || strings.HasSuffix(installedTo, ".new") {
		appendMsg(ui.Yellow("! Windows: binary baru ditulis di " + installedTo))
		appendMsg(ui.Dim("  Tutup kartika, lalu ganti binary lama dengan berkas tersebut agar berlaku."))
		return
	}
	appendMsg(ui.Bold(ui.Green("✓ Terpasang: " + installedTo)))
	appendMsg(ui.Dim("  Versi dilaporkan: " + rel.Tag))
	appendMsg(ui.Dim("  Binary lama sudah digantikan — jalankan ulang 'kartika' untuk memakai versi baru."))
	if !strings.Contains(os.Getenv("PATH"), filepath.Dir(installedTo)) {
		appendMsg(ui.Yellow("! " + filepath.Dir(installedTo) + " belum ada di PATH — tambahkan agar perintah 'kartika' menunjuk versi baru."))
	}
}

// buildUlangSource membangun binary dari kode sumber lokal (opsi build ulang)
// lalu memasangnya menggantikan binary yang sedang berjalan.
func buildUlangSource(srcDir, tag string, appendMsg func(string)) {
	appendMsg("")
	if !update.IsSourceDir(srcDir) {
		appendMsg(ui.Red("✗ Direktori sumber tidak ditemukan (" + srcDir + ")."))
		appendMsg(ui.Dim("  Jalankan /update dari dalam repo, atau pilih pasang binary rilis."))
		return
	}
	if _, err := exec.LookPath("go"); err != nil {
		appendMsg(ui.Red("✗ Toolchain Go tidak ditemukan di PATH."))
		appendMsg(ui.Dim("  Pasang Go ≥ 1.21 (https://go.dev/dl), atau pilih pasang binary rilis."))
		return
	}
	dst, err := update.CurrentExecutable()
	if err != nil {
		appendMsg(ui.Red("✗ Tidak dapat menentukan lokasi binary berjalan: " + err.Error()))
		return
	}

	tmpDir, err := os.MkdirTemp("", "kartika-build-")
	if err != nil {
		appendMsg(ui.Red("✗ Gagal membuat direktori sementara: " + err.Error()))
		return
	}
	defer os.RemoveAll(tmpDir)
	out := filepath.Join(tmpDir, "kartika")

	appendMsg("  Membangun dari source: " + srcDir)
	ver := strings.TrimPrefix(tag, "v")
	if err := update.BuildFromSource(srcDir, out, ver); err != nil {
		appendMsg(ui.Red("✗ Build gagal:"))
		for _, l := range strings.Split(err.Error(), "\n") {
			appendMsg(ui.Dim("    " + l))
		}
		return
	}
	appendMsg(ui.Green("✓ Build selesai: " + out))

	installedTo, err := update.Install(out, dst)
	if err != nil {
		appendMsg(ui.Red("✗ Gagal memasang hasil build: " + err.Error()))
		appendMsg(ui.Dim("  Saran: jalankan 'bash build.sh' lalu 'bash install.sh' dengan hak yang cukup."))
		return
	}
	appendMsg("")
	appendMsg(ui.Bold(ui.Green("✓ Terpasang: " + installedTo)))
	appendMsg(ui.Dim("  Jalankan ulang 'kartika' untuk memakai binary hasil build."))
}

// updateBanner mengembalikan satu baris pemberitahuan bila ada rilis lebih
// baru (dicek di latar belakang, cache 6 jam, tidak pernah menggagalkan
// startup). Mengembalikan "" bila tidak ada pembaruan / pemeriksaan mati.
func updateBanner() string {
	if !update.Enabled() {
		return ""
	}
	type hasil struct {
		rel *update.Release
		err error
	}
	ch := make(chan hasil, 1)
	go func() {
		rel, err := update.LatestCached()
		ch <- hasil{rel, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil || r.rel == nil || !update.Newer(version, r.rel.Tag) {
			return ""
		}
		msg := ui.Yellow("↑ Versi baru tersedia: v" + version + " → " + r.rel.Tag)
		msg += ui.Dim("  · ketik /update untuk memasang")
		return msg
	case <-time.After(2 * time.Second):
		return "" // jangan pernah menahan startup demi cek versi
	}
}

// reportFailure menerjemahkan kegagalan chat menjadi pesan operator yang
// berguna: kegagalan konfigurasi menampilkan panduan letak API/model
// (sekali per sesi), bukan error jaringan mentah.
func reportFailure(err error, cfg *config.Config, showConfig func(string)) {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "koneksi ke engine gagal"):
		showConfig("API atau model belum dikonfigurasi — engine tidak dapat dihubungi.")
	case strings.Contains(msg, "HTTP 401"), strings.Contains(msg, "HTTP 403"):
		showConfig("Kunci API ditolak — KARTIKA_API_KEY belum diisi atau salah. Ketik /konfigurasi-api-key.")
	case strings.Contains(msg, "HTTP 404"):
		showConfig("Model '" + cfg.Model + "' tidak ditemukan pada endpoint (" + cfg.APIURL + ") — ketik /pilih model untuk memilih dari daftar model yang tersedia.")
	case strings.Contains(msg, "HTTP 400"):
		showConfig("Engine menolak permintaan (HTTP 400). Model '" + cfg.Model + "' mungkin tidak valid — ketik /pilih model.")
	default:
		ui.Fail("CHAT GAGAL: " + msg)
	}
}

// loadMCP memuat seluruh MCP Server yang didefinisikan pada mcp.json dan
// mendaftarkan seluruh tools-nya ke registri dengan prefiks mcp_<server>_.
func loadMCP(cfg *config.Config, reg *tools.Registry) []*mcp.Client {
	path := cfg.MCPConfig
	if _, err := os.Stat(path); err != nil {
		ui.Debug("Konfigurasi MCP '" + path + "' tidak ditemukan — modul eksternal dilewati.")
		return nil
	}
	ui.Debug("Memuat konfigurasi MCP: " + path)
	servers, err := mcp.LoadConfig(path)
	if err != nil {
		ui.Warn("Konfigurasi MCP tidak valid: " + err.Error())
		return nil
	}
	names := make([]string, 0, len(servers))
	for n := range servers {
		names = append(names, n)
	}
	sort.Strings(names)

	var clients []*mcp.Client
	for _, name := range names {
		cl, err := mcp.Start(name, servers[name])
		if err != nil {
			ui.MCPFail(name, err.Error())
			continue
		}
		registerMCPTools(reg, cl)
		clients = append(clients, cl)
		ui.MCPOnline(name, cl.Server, cl.Version, len(cl.Tools))
	}
	return clients
}

// registerMCPTools mendaftarkan seluruh tool dari satu MCP server.
func registerMCPTools(reg *tools.Registry, cl *mcp.Client) {
	for _, t := range cl.Tools {
		toolName := t.Name
		client := cl
		desc := t.Description
		if desc == "" {
			desc = "Modul MCP dari server '" + cl.Name + "'"
		}
		schema := t.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		reg.Register(&tools.Tool{
			Name:        mcpToolRegName(cl.Name, toolName),
			Description: "[MCP:" + cl.Name + "] " + desc,
			Schema:      schema,
			Fn: func(raw json.RawMessage) string {
				out, isErr, err := client.CallTool(toolName, raw)
				if err != nil {
					return "ERROR MCP (" + client.Name + "/" + toolName + "): " + err.Error()
				}
				if isErr {
					return "[MCP tool melaporkan kegagalan]\n" + out
				}
				return out
			},
		})
	}
}

// mcpToolRegName menyandikan nama modul MCP menjadi nama fungsi yang valid
// untuk engine (pola: mcp_<server>_<tool>, karakter aman, maksimum 60 karakter).
func mcpToolRegName(server, tool string) string {
	join := strings.ToLower("mcp_" + server + "_" + tool)
	var b strings.Builder
	for _, r := range join {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	s := b.String()
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}

func printStatusDetail(cfg *config.Config, reg *tools.Registry, clients []*mcp.Client, missions, builtinCount int) {
	ui.Info("")
	ui.Info(ui.Bold(ui.Cyan("STATUS OPERASI")))
	ui.Info(fmt.Sprintf("  ENGINE   : %s @ %s", ui.Bold(cfg.Model), cfg.APIURL))
	if cfg.Provider != "" {
		ui.Info(fmt.Sprintf("  PROVIDER : %s", cfg.Provider))
	}
	up := checkEngine(cfg)
	st := ui.Green("TERHUBUNG")
	if !up {
		st = ui.Red("TAK TERHUBUNG")
	}
	ui.Info(fmt.Sprintf("  KUNCI API: %s | ENGINE: %s", keyStatus(cfg.APIKey), st))
	ui.Info(fmt.Sprintf("  KONFIG   : %s | %s", globalEnvPath(), ui.Dim("isi: KARTIKA_API_URL, KARTIKA_API_KEY, KARTIKA_MODEL")))
	ui.Info(fmt.Sprintf("  BATAS    : %d langkah penalaran/chat | timeout engine %ds", cfg.MaxSteps, cfg.TimeoutSec))
	ui.Info(fmt.Sprintf("  MODUL    : %d terdaftar (%d built-in + %d modul MCP)", reg.Count(), builtinCount, reg.Count()-builtinCount))
	ui.Info(fmt.Sprintf("  MCP      : %d server online", len(clients)))
	ui.Info(fmt.Sprintf("  CHAT     : %d chat dijalankan pada sesi ini", missions))
	ui.Info("")
}

// keyStatus melaporkan status kunci API tanpa menampilkan nilainya.
func keyStatus(k string) string {
	if k == "" || k == "ollama" {
		return ui.Yellow("belum diisi")
	}
	return ui.Green("diisi (" + k[:3] + "***)")
}

// globalEnvPath mengembalikan path konfigurasi global dengan status keberadaan.
func globalEnvPath() string {
	p := ""
	if home, err := os.UserHomeDir(); err == nil {
		p = home + "/.config/kartika/.env"
	}
	if _, err := os.Stat(p); err == nil {
		return ui.Bold(p + " (ada)")
	}
	return ui.Bold(p) + ui.Red(" (belum ada)")
}

// printMCPStatus menampilkan server MCP online dan cara menambahkannya.
func printMCPStatus(cfg *config.Config, clients []*mcp.Client) {
	ui.Info("")
	ui.Info(ui.Bold(ui.Cyan("STATUS MCP")))
	ui.Info(fmt.Sprintf("  KONFIG : %s", ui.Bold(cfg.MCPConfig)))
	if len(clients) == 0 {
		ui.Info("  Server : (tidak ada yang online)")
		ui.Info(ui.Dim("  Salin mcp.json.example menjadi mcp.json lalu isi perintah server MCP."))
	} else {
		for _, cl := range clients {
			ui.Info(fmt.Sprintf("  %s %s — %s v%s (%d tools)", ui.Green("[ONLINE]"), ui.Bold(cl.Name), cl.Server, cl.Version, len(cl.Tools)))
		}
	}
	ui.Info("")
}

// printSkillList menampilkan pustaka skill metodologi (/skill tanpa argumen).
func printSkillList() {
	skills := tools.SkillsAll()
	if len(skills) == 0 {
		ui.Info("")
		ui.Info("Pustaka skill kosong — direktori: " + tools.SkillsRoot())
		ui.Info(ui.Dim("  Unduh: git clone https://github.com/murrtada/bug-bounty-agent-skills.git \"" + tools.SkillsRoot() + "/bug-bounty-agent-skills\""))
		ui.Info("")
		return
	}
	ui.Info("")
	ui.Info(ui.Bold(ui.Cyan(fmt.Sprintf("PUSTAKA SKILL METODOLOGI — %d skill @ %s", len(skills), tools.SkillsRoot()))))
	for _, s := range skills {
		desc := s.Description
		if rn := []rune(desc); len(rn) > 130 {
			desc = string(rn[:130]) + "…"
		}
		ui.Info(fmt.Sprintf("  %s %s", ui.Bold(s.Name), ui.Dim(desc)))
	}
	ui.Info("")
	ui.Info(ui.Dim("  Baca metodologi lengkap satu skill: /skill <nama>  (mis. /skill hunt-ssrf)"))
	ui.Info("")
}

// printSkillDoc menampilkan isi satu skill (/skill <nama>).
func printSkillDoc(name string) {
	s, doc, ok := tools.SkillDoc(name, 8000)
	if !ok {
		ui.Warn("Skill '" + name + "' tidak ditemukan — ketik /skill untuk daftar seluruh skill.")
		return
	}
	ui.Info("")
	ui.Info(ui.Bold(ui.Cyan("SKILL: "+s.Name)) + " " + ui.Dim("("+s.Path+")"))
	ui.Info("")
	ui.Info(doc)
	ui.Info("")
	ui.Info(ui.Dim("  Engine membaca metodologi penuh via modul skill_read / skill_search."))
	ui.Info("")
}

func printModules(reg *tools.Registry) {
	ui.Info("")
	ui.Info(ui.Bold(ui.Cyan("REGISTRI MODUL TAKTIS")))
	for _, n := range reg.Names() {
		if strings.HasPrefix(n, "mcp_") {
			ui.Info(fmt.Sprintf("  - %s %s", ui.Bold(n), ui.Dim("(MCP)")))
		} else {
			ui.Info(fmt.Sprintf("  - %s %s", ui.Bold(n), ui.Dim("(built-in)")))
		}
	}
	ui.Info("")
}
