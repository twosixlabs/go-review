package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/russellbrasser/go-review/internal/intelligence"
	"github.com/russellbrasser/go-review/internal/review"
	"github.com/russellbrasser/go-review/internal/workbench"
)

type invocation struct {
	addr, dir, docs, saved string
	review                 bool
}

func parseInvocation(args []string) (invocation, error) {
	inv := invocation{}
	command := ""
	if len(args) > 0 && (args[0] == "review" || args[0] == "open") {
		command, args = args[0], args[1:]
	}
	flags := flag.NewFlagSet("go-review", flag.ContinueOnError)
	flags.StringVar(&inv.addr, "addr", "127.0.0.1:8090", "loopback HTTP address")
	flags.StringVar(&inv.dir, "dir", "", "Go module for static exploration (does not execute tests)")
	flags.StringVar(&inv.docs, "docs", "", "optional documentation directory for static exploration")
	if err := flags.Parse(args); err != nil {
		return inv, err
	}
	if command != "" {
		if flags.NArg() != 1 || inv.dir != "" || inv.docs != "" {
			return inv, fmt.Errorf("usage: go-review %s [-addr 127.0.0.1:8090] <path>", command)
		}
		if command == "review" {
			inv.review, inv.dir = true, flags.Arg(0)
		} else {
			inv.saved = flags.Arg(0)
		}
	} else if flags.NArg() != 0 {
		return inv, fmt.Errorf("use go-review review <repository>")
	}
	return inv, nil
}

func main() {
	inv, err := parseInvocation(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}
	host, _, err := net.SplitHostPort(inv.addr)
	if err != nil {
		log.Fatal(err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		log.Fatal("-addr must use a loopback IP address")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var report *review.Report
	var completed *review.RepositoryReview
	if inv.review {
		runCtx, cancel := context.WithTimeout(ctx, time.Hour)
		fmt.Println("Review executes repository tests and scripts. Run only on a trusted checkout.")
		completed, err = review.ReviewRepository(runCtx, inv.dir, "", func(p review.ReviewProgress) {
			fmt.Printf("%s · %s\n", p.Phase, p.Module)
		})
		cancel()
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Review %s. Retained: %s\n", completed.Status, filepath.Join(completed.Directory, "review.json"))
	} else if inv.saved != "" {
		data, e := os.ReadFile(inv.saved)
		if e != nil {
			log.Fatal(e)
		}
		if e = json.Unmarshal(data, &completed); e != nil {
			log.Fatal(e)
		}
		if completed == nil || len(completed.Modules) == 0 {
			log.Fatal("not a retained repository review")
		}
	} else if inv.dir != "" {
		runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		report, err = review.Analyze(runCtx, review.Options{Dir: inv.dir, DocumentationDir: inv.docs})
		cancel()
		if err != nil {
			log.Fatal(err)
		}
	}
	if ctx.Err() != nil {
		log.Fatal(ctx.Err())
	}
	listener, err := net.Listen("tcp", inv.addr)
	if err != nil {
		log.Fatal(err)
	}
	url := "http://" + listener.Addr().String()
	fmt.Printf("Go Review: %s\nBrowsing does not execute tests. Use Review repository for the complete review. Ctrl-C to stop.\n", url)
	work := workbench.New(report)
	if completed != nil {
		work = workbench.NewReviewed(completed)
	}
	agentPath, err := intelligence.ConfigPath()
	if err != nil {
		log.Fatal(err)
	}
	agentConfig, err := intelligence.LoadConfig(agentPath)
	if err != nil {
		log.Fatal(err)
	}
	work.ConfigureAgent(agentConfig)
	server := &http.Server{Handler: work.Handler(), ReadHeaderTimeout: 10 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			log.Printf("stop review server: %v", err)
		}
	}()
	if inv.review || inv.saved != "" {
		var command *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			command = exec.Command("open", url)
		case "windows":
			command = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
		default:
			command = exec.Command("xdg-open", url)
		}
		if e := command.Run(); e != nil {
			log.Printf("open browser: %v; use %s", e, url)
		}
	}
	err = server.Serve(listener)
	stop()
	<-shutdownDone
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
