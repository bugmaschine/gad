package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"bugmaschine/gad/pkg/chrome"
	"bugmaschine/gad/pkg/dirs"
	"bugmaschine/gad/pkg/download"
	"bugmaschine/gad/pkg/logger"
	"bugmaschine/gad/pkg/utils"

	"github.com/chromedp/chromedp"
	"golang.design/x/hotkey"
	"golang.design/x/hotkey/mainthread"
	"golang.org/x/term"
)

func main() {
	mainthread.Init(appMain)
}

func appMain() {
	os.Exit(run())
}

func run() int {
	queueFile := flag.String("queue", "", "Path to a queue file with one URL per line (required)")
	outputFile := flag.String("output", "triage_results.txt", "Path to write accepted/rejected URLs to")
	restart := flag.Bool("restart", false, "Ignore any existing -output file and start the triage over from scratch")
	debug := flag.Bool("debug", false, "Enable debug logging")
	parse := flag.String("parse", "", "just parse the file and write the cleaned version")
	flag.Parse()

	// so basically we loop through the processed file which was written to otputFile. and then only save the accepted stuff into our parsed stuff.

	if *parse != "" {
		f, err := os.Open(*outputFile)
		if err != nil {
			slog.Error("Failed to open file for parsing", "error", err, "path", *parse)
			return 1
		}
		defer f.Close()

		// read the whole file into an array, where newline marks an entry
		var lines []string
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			lines = append(lines, line)
		}
		if err := scanner.Err(); err != nil {
			slog.Error("Failed to read file for parsing", "error", err, "path", *parse)
			return 1
		}

		// only keep the accepted entries
		var parsed []string
		for _, line := range lines {
			parts := strings.SplitN(line, "\t", 2)
			if len(parts) != 2 {
				slog.Warn("Skipping malformed line", "line", line)
				continue
			}

			status, url := parts[0], parts[1]
			if status == "ACCEPTED" {
				parsed = append(parsed, url)
			}
		}

		// write parsed array to parsed txt file
		out := strings.Join(parsed, "\n")
		if err := os.WriteFile(*parse, []byte(out), 0644); err != nil {
			slog.Error("Failed to write file", "error", err)
			return 1
		}
		fmt.Println("written proccesed info to ", *parse)
		fmt.Println("done!")
		return 0
	}

	if *queueFile == "" {
		fmt.Fprintln(os.Stderr, "Error: -queue is required")
		flag.Usage()
		return 1
	}

	logger.InitDefaultLogger(*debug, "")
	defer logger.Close()

	dataDir, err := dirs.GetDataDir()
	if err != nil {
		slog.Error("Failed to create data directory", "error", err)
		return 1
	}

	ctx, stop := interruptContext(context.Background())
	// stop isn't safe to call twice (it closes a channel) - both the normal
	// defer below and the raw-mode Ctrl+C handler may want to trigger it, so
	// route both through the same sync.Once.
	var stopOnce sync.Once
	safeStop := func() { stopOnce.Do(stop) }
	defer safeStop()

	// Downloader is only needed so chrome.Manager can fetch a browser binary
	// todo: move this into reusable function, as we call it 2-3 times.
	assetDownloader := download.NewDownloader("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36", *debug, 0)
	defer assetDownloader.Shutdown()

	chromeMgr := chrome.NewManager(dataDir, assetDownloader)

	slog.Info("Starting browser...")
	// headless is forced false. how else are you going to see the page to decide y/n?
	scrapeCtx, cancel, err := chromeMgr.Get(ctx, false, *debug)
	if err != nil {
		slog.Error("Failed to start browser", "error", err)
		return 1
	}
	defer cancel()

	// register hotkeys.
	hkY := hotkey.New(nil, hotkey.KeyY)
	if err := hkY.Register(); err != nil {
		slog.Error("Failed to register 'y' hotkey", "error", err)
		return 1
	}
	defer hkY.Unregister()

	hkN := hotkey.New(nil, hotkey.KeyN)
	if err := hkN.Register(); err != nil {
		slog.Error("Failed to register 'n' hotkey", "error", err)
		return 1
	}
	defer hkN.Unregister()

	hkB := hotkey.New(nil, hotkey.KeyB)
	if err := hkB.Register(); err != nil {
		slog.Error("Failed to register 'b' hotkey", "error", err)
		return 1
	}
	defer hkB.Unregister()

	// according to the package example, you have to call it like this. don't know why.
	decision := make(chan string, 1)
	go listenHotkeyDecision(ctx, hkY, "y", decision)
	go listenHotkeyDecision(ctx, hkN, "n", decision)
	go listenHotkeyDecision(ctx, hkB, "b", decision)

	// this stuff is here so you can press y/n/b even on wayland.
	if stdinFd := int(os.Stdin.Fd()); term.IsTerminal(stdinFd) {
		oldState, err := term.MakeRaw(stdinFd)
		if err != nil {
			slog.Warn("Failed to put stdin into raw mode - console y/n/b will need Enter", "error", err)
		} else {
			defer term.Restore(stdinFd, oldState)
			go listenStdinDecision(ctx, safeStop, decision)
		}
	} else {
		slog.Debug("stdin is not a terminal, skipping raw console y/n/b listener")
	}

	// Replay any decisions already recorded in -output from a previous run,
	// so we can pick up where we left off. -restart throws that away.
	priorDecisions, err := loadPriorDecisions(*outputFile)
	if err != nil {
		slog.Error("Failed to read prior results", "error", err, "path", *outputFile)
		return 1
	}

	openFlags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if *restart {
		openFlags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
		priorDecisions = map[string]string{}
	}

	results, err := os.OpenFile(*outputFile, openFlags, 0644)
	if err != nil {
		slog.Error("Failed to open output file", "error", err, "path", *outputFile)
		return 1
	}
	defer results.Close()
	resultsWriter := bufio.NewWriter(results)
	defer resultsWriter.Flush()

	accepted, rejected, triageErr := triageQueueURLs(ctx, scrapeCtx, *queueFile, resultsWriter, decision, priorDecisions)

	fmt.Println("\n=== Accepted URLs ===")
	if len(accepted) == 0 {
		fmt.Println("(none)")
	}
	for _, u := range accepted {
		fmt.Println(u)
	}

	fmt.Println("\n=== Rejected URLs ===")
	if len(rejected) == 0 {
		fmt.Println("(none)")
	}
	for _, u := range rejected {
		fmt.Println(u)
	}

	if triageErr != nil {
		if isCancellation(ctx, triageErr) {
			fmt.Println("\nCancelled - partial results written to", *outputFile)
			return 130
		}
		slog.Error("Triage failed", "error", triageErr)
		return 1
	}

	fmt.Println("\nResults written to", *outputFile)
	return 0
}

// this is basically run per hotkey
func listenHotkeyDecision(ctx context.Context, hk *hotkey.Hotkey, label string, decision chan<- string) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-hk.Keydown():
		}

		select {
		case decision <- label:
		default:
		}

		select {
		case <-ctx.Done():
			return
		case <-hk.Keyup():
		}
	}
}

func listenStdinDecision(ctx context.Context, stop func(), decision chan<- string) {
	buf := make([]byte, 1)
	for {
		if ctx.Err() != nil {
			return
		}

		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			return
		}

		switch buf[0] {
		case 'y', 'Y':
			select {
			case decision <- "y":
			default:
			}
		case 'n', 'N':
			select {
			case decision <- "n":
			default:
			}
		case 'b', 'B':
			select {
			case decision <- "b":
			default:
			}
		case 0x03: // Ctrl+C, no longer delivered as SIGINT once raw mode is on
			stop()
			return
		}
	}
}

// resultLabels maps the "y"/"n" decision values used internally to the
// labels written to (and read back from) the results file.
var resultLabels = map[string]string{
	"y": "ACCEPTED",
	"n": "REJECTED",
}

// loadPriorDecisions reads an existing results file, if any, and returns a
// url -> "y"/"n" map reflecting the most recent decision recorded for each
// URL. Results are appended rather than rewritten in place, so a URL can
// appear more than once if it was reclassified in an earlier run; since we
// scan top to bottom and simply overwrite the map entry each time, whichever
// line came last always wins, which is what we want.
func loadPriorDecisions(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	decisions := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		label, url, ok := strings.Cut(scanner.Text(), "\t")
		if !ok {
			continue
		}
		switch label {
		case "ACCEPTED":
			decisions[url] = "y"
		case "REJECTED":
			decisions[url] = "n"
		}
	}
	return decisions, scanner.Err()
}

// splitDecisions walks urls in order and buckets each one into accepted or
// rejected according to decisions, skipping any URL with no decision yet.
// Computing the summary this way (rather than appending to accepted/rejected
// as each decision is made) is what makes "go back and reclassify" safe: a
// changed decisions[url] is automatically reflected here without needing to
// hunt down and mutate an earlier slice entry.
func splitDecisions(urls []string, decisions map[string]string) (accepted, rejected []string) {
	for _, u := range urls {
		switch decisions[u] {
		case "y":
			accepted = append(accepted, u)
		case "n":
			rejected = append(rejected, u)
		}
	}
	return accepted, rejected
}

// triageQueueURLs loads the deduplicated URL list from queueFile, skips past
// any URL that decisions already has an answer for (i.e. resuming earlier
// progress), and then walks the rest: navigating scrapeCtx's Chrome window to
// each one and waiting for a y/n/b decision on the shared channel fed by
// listenHotkeyDecision and listenStdinDecision. y/n record a decision and
// move to the next URL; b steps back to the previous one so it can be shown
// again and reclassified. Every new or changed decision is appended to
// resultsWriter immediately, so a crash or Ctrl+C never loses progress - and
// on the next run, loadPriorDecisions replays the file to reconstruct where
// things stood.
func triageQueueURLs(ctx context.Context, scrapeCtx context.Context, queueFile string, resultsWriter *bufio.Writer, decision <-chan string, decisions map[string]string) (accepted, rejected []string, err error) {
	f, err := os.Open(queueFile)
	if err != nil {
		return nil, nil, err
	}
	urls, err := utils.LoadQueueURLs(bufio.NewScanner(f))
	f.Close()
	if err != nil {
		return nil, nil, err
	}

	start := 0
	for start < len(urls) {
		if _, done := decisions[urls[start]]; !done {
			break
		}
		start++
	}
	switch {
	case start > 0 && start < len(urls):
		fmt.Printf("Resuming: %d of %d URLs already triaged, %d remaining.\n", start, len(urls), len(urls)-start)
	case start > 0 && start == len(urls):
		fmt.Printf("All %d URLs already triaged, nothing left to do.\n", len(urls))
	}

	i := start
	for i < len(urls) {
		if ctxErr := ctx.Err(); ctxErr != nil {
			accepted, rejected = splitDecisions(urls, decisions)
			return accepted, rejected, ctxErr
		}

		url := urls[i]
		fmt.Printf("\nOpening (%d/%d): %s\n", i+1, len(urls), url)
		if navErr := chromedp.Run(scrapeCtx, chromedp.Navigate(url)); navErr != nil {
			slog.Warn("Failed to open URL in browser", "url", url, "error", navErr)
		}

		// drop any stray y/n/b presses that landed before this URL was shown
		drainStringChan(decision)

		if prior, ok := decisions[url]; ok {
			fmt.Printf("(currently: %s)\n", resultLabels[prior])
		}
		if i > 0 {
			fmt.Print("Press Y to keep, N to skip, B to go back (Chrome or console, no Enter needed)...\r\n")
		} else {
			fmt.Print("Press Y to keep, N to skip (Chrome or console, no Enter needed)...\r\n")
		}

		select {
		case <-ctx.Done():
			accepted, rejected = splitDecisions(urls, decisions)
			return accepted, rejected, ctx.Err()
		case d := <-decision:
			switch d {
			case "y", "n":
				if decisions[url] != d {
					decisions[url] = d
					writeResult(resultsWriter, resultLabels[d], url)
				}
				i++
			case "b":
				if i > 0 {
					i--
				} else {
					fmt.Println("Already at the first URL.")
				}
			}
		}
	}

	accepted, rejected = splitDecisions(urls, decisions)
	return accepted, rejected, nil
}

func writeResult(w *bufio.Writer, label, url string) {
	fmt.Fprintf(w, "%s\t%s\n", label, url)
	w.Flush()
}

func drainStringChan(ch <-chan string) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

func interruptContext(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	signals := make(chan os.Signal, 2)
	done := make(chan struct{})

	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	go func() {
		defer close(done)

		sig, ok := <-signals
		if !ok {
			return
		}

		slog.Warn("Shutdown requested, finishing cleanup. Press Ctrl+C again to force exit.", "signal", sig)
		cancel()

		sig, ok = <-signals
		if !ok {
			return
		}

		slog.Error("Second interrupt received, forcing exit", "signal", sig)
		os.Exit(130)
	}()

	return ctx, func() {
		signal.Stop(signals)
		close(signals)
		cancel()
		<-done
	}
}

func isCancellation(ctx context.Context, err error) bool {
	return ctx.Err() != nil ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
}
