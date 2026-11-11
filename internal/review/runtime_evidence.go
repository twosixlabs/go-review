package review

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

type RuntimeEvidence struct {
	Status       string               `json:"status"`
	Error        string               `json:"error,omitempty"`
	SourceSHA256 string               `json:"sourceSHA256"`
	TraceSHA256  string               `json:"traceSHA256,omitempty"`
	ToolVersion  string               `json:"toolVersion,omitempty"`
	Format       string               `json:"format,omitempty"`
	ParsedPath   string               `json:"parsedPath,omitempty"`
	Observations []RuntimeObservation `json:"observations"`
	Limitations  []string             `json:"limitations"`
}

type RuntimeFrame struct {
	Function string    `json:"function"`
	File     string    `json:"file"`
	Line     int       `json:"line"`
	Location *Location `json:"location,omitempty"`
	SymbolID string    `json:"symbolID,omitempty"`
}

// RuntimeObservation is a scheduler relationship with event-associated stacks,
// not a function-entry log or proof of the contents of a channel message.
type RuntimeObservation struct {
	Kind             string         `json:"kind"`
	TimeNS           int64          `json:"timeNS"`
	Goroutine        string         `json:"goroutine"`
	RelatedGoroutine string         `json:"relatedGoroutine"`
	Reason           string         `json:"reason,omitempty"`
	Stack            []RuntimeFrame `json:"stack"`
	TargetStack      []RuntimeFrame `json:"targetStack"`
	ComponentIDs     []string       `json:"componentIDs"`
	ExchangeIDs      []string       `json:"exchangeIDs"`
}

func collectStandardRuntime(ctx context.Context, r *Report, run *UnitTestRun, sourceSHA string) (err error) {
	evidence := &RuntimeEvidence{Status: "error", SourceSHA256: sourceSHA, Observations: []RuntimeObservation{}, Limitations: []string{
		"Scheduler events have associated stacks, not every function call or an exhaustive call order.",
		"Launch, blocking and wakeup goroutine IDs retain concurrent relationships; wakeup does not identify a message or its contents.",
		"Exchange links are source-line candidates at asynchronous boundaries; multiple candidates remain ambiguous.",
	}}
	run.Runtime = evidence
	defer func() {
		if err != nil {
			evidence.Error = err.Error()
		}
	}()
	if sourceSHA != r.SourceSHA256 {
		return fmt.Errorf("standard runtime trace belongs to a different source snapshot")
	}
	raw, err := os.ReadFile(run.RuntimeTracePath)
	if err != nil {
		return fmt.Errorf("read standard runtime trace: %w", err)
	}
	evidence.TraceSHA256 = digest(raw)
	version, err := runGo(ctx, r, r.Root, true, "version")
	if err != nil {
		return fmt.Errorf("standard runtime tool version: %w", err)
	}
	evidence.ToolVersion = strings.TrimSpace(string(version))
	data, decodeErr := runGo(ctx, r, r.Root, false, "tool", "trace", "-d=parsed", run.RuntimeTracePath)
	evidence.ParsedPath = filepath.Join(filepath.Dir(run.RuntimeTracePath), "runtime-events.txt")
	if err = os.WriteFile(evidence.ParsedPath, data, 0600); err != nil {
		return fmt.Errorf("retain standard runtime events: %w", err)
	}
	if decodeErr != nil {
		return fmt.Errorf("standard Go trace decoding unavailable (raw trace retained): %w", decodeErr)
	}
	events, err := parseStandardRuntime(strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	evidence.Format = "go-tool-trace-parsed/goid-v1"
	evidence.Observations = runtimeObservations(r, events)
	evidence.Status = "available"
	return nil
}

type standardRuntimeEvent struct {
	time                      int64
	current, target, from, to string
	reason                    string
	stack, transition         []RuntimeFrame
}

// This adapter covers the inspected GoID-form parsed output, not Go's binary
// format. Other formats fail visibly while raw traces and coverage survive.
var standardTransition = regexp.MustCompile(`^M=-?\d+ P=-?\d+ G=(-?\d+) StateTransition Time=(\d+) GoID=(\d+) ([A-Za-z]+)->([A-Za-z]+) Reason=("(?:[^"\\]|\\.)*")$`)

func parseStandardRuntime(input io.Reader) ([]standardRuntimeEvent, error) {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	var events []standardRuntimeEvent
	var current *standardRuntimeEvent
	section, pending := "", ""
	transitions := 0
	for scanner.Scan() {
		line := scanner.Text()
		if pending != "" && !strings.HasPrefix(line, "\t\t") {
			return nil, fmt.Errorf("truncated standard runtime stack after %q", pending)
		}
		if strings.HasPrefix(line, "M=") {
			if current != nil {
				events = append(events, *current)
			}
			current, section = nil, ""
			if !strings.Contains(line, "StateTransition") || !strings.Contains(line, "GoID=") {
				continue
			}
			match := standardTransition.FindStringSubmatch(line)
			if match == nil {
				return nil, fmt.Errorf("unsupported standard runtime transition: %s", line)
			}
			time, err := strconv.ParseInt(match[2], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("standard runtime timestamp: %w", err)
			}
			reason, err := strconv.Unquote(match[6])
			if err != nil {
				return nil, fmt.Errorf("standard runtime reason: %w", err)
			}
			current = &standardRuntimeEvent{time: time, current: match[1], target: match[3], from: match[4], to: match[5], reason: reason}
			transitions++
			continue
		}
		if current == nil {
			continue
		}
		if line == "Stack=" || line == "TransitionStack=" {
			section = line
			continue
		}
		if section == "" || strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, "\t\t") {
			location := strings.TrimSpace(line)
			colon := strings.LastIndex(location, ":")
			if colon < 0 || pending == "" {
				return nil, fmt.Errorf("unsupported standard runtime stack location: %s", line)
			}
			path, number := location[:colon], location[colon+1:]
			n, err := strconv.Atoi(number)
			if err != nil || n <= 0 {
				return nil, fmt.Errorf("invalid standard runtime stack line %q", line)
			}
			frame := RuntimeFrame{Function: pending, File: path, Line: n}
			if section == "Stack=" {
				current.stack = append(current.stack, frame)
			} else {
				current.transition = append(current.transition, frame)
			}
			pending = ""
		} else if name, _, found := strings.Cut(strings.TrimSpace(line), " @ "); found {
			pending = name
		} else {
			return nil, fmt.Errorf("unsupported standard runtime stack: %s", line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read standard runtime events: %w", err)
	}
	if pending != "" {
		return nil, fmt.Errorf("truncated standard runtime stack after %q", pending)
	}
	if current != nil {
		events = append(events, *current)
	}
	if transitions == 0 {
		return nil, fmt.Errorf("unsupported standard runtime event format: no GoID-form transitions")
	}
	return events, nil
}

func runtimeObservations(r *Report, events []standardRuntimeEvent) []RuntimeObservation {
	files := map[string]File{}
	for _, file := range r.Files {
		if strings.HasSuffix(file.Path, "_test.go") {
			continue
		}
		path := filepath.Join(r.Root, file.Path)
		files[filepath.Clean(path)] = file
		if canonical, err := filepath.EvalSymlinks(path); err == nil {
			files[canonical] = file
		}
	}
	link := func(frames []RuntimeFrame) []RuntimeFrame {
		result := []RuntimeFrame{}
		for _, frame := range frames {
			file, found := files[filepath.Clean(frame.File)]
			if !found {
				continue
			}
			loc := Location{FileID: file.ID, Line: frame.Line, EndLine: frame.Line, Column: 1, EndColumn: 1}
			frame.Location = &loc
			for _, symbol := range r.Symbols {
				if symbol.FileID == file.ID && (symbol.Kind == "function" || symbol.Kind == "method" || symbol.Kind == "closure") &&
					symbol.Location.Line <= frame.Line && symbol.Location.EndLine >= frame.Line {
					frame.SymbolID = symbol.ID
					break
				}
			}
			result = append(result, frame)
		}
		return result
	}
	flow := r.SystemFlow
	if flow == nil {
		flow = BuildSystemFlow(r)
	}
	result := []RuntimeObservation{}
	waiting := map[string][]RuntimeFrame{}
	for _, event := range events {
		kind := ""
		switch {
		case event.from == "NotExist" && event.to == "Runnable":
			kind = "launch"
		case event.from == "Running" && event.to == "Waiting":
			kind = "block"
		case event.from == "Waiting" && event.to == "Runnable":
			kind = "wakeup"
		default:
			continue
		}
		observation := RuntimeObservation{Kind: kind, TimeNS: event.time, Goroutine: event.target,
			RelatedGoroutine: event.current, Reason: event.reason, Stack: link(event.stack),
			TargetStack: link(event.transition), ComponentIDs: []string{}, ExchangeIDs: []string{}}
		if kind == "block" {
			waiting[event.target] = observation.TargetStack
		}
		if kind == "wakeup" {
			observation.TargetStack = waiting[event.target]
			delete(waiting, event.target)
		}
		if len(observation.Stack)+len(observation.TargetStack) == 0 {
			continue
		}
		for _, frame := range append(append([]RuntimeFrame{}, observation.Stack...), observation.TargetStack...) {
			for _, operation := range flow.Operations {
				if operation.SymbolID == frame.SymbolID && !slices.Contains(observation.ComponentIDs, operation.ComponentID) {
					observation.ComponentIDs = append(observation.ComponentIDs, operation.ComponentID)
				}
			}
		}
		if kind == "launch" {
			for _, exchange := range flow.Exchanges {
				if exchange.Kind != "asynchronous" {
					continue
				}
				for _, location := range exchange.Sources {
					for _, frame := range observation.Stack {
						if frame.Location.FileID == location.FileID && frame.Line >= location.Line && frame.Line <= location.EndLine &&
							!slices.Contains(observation.ExchangeIDs, exchange.ID) {
							observation.ExchangeIDs = append(observation.ExchangeIDs, exchange.ID)
						}
					}
				}
			}
		}
		result = append(result, observation)
	}
	return result
}
