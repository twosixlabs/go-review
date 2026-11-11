// Command go-review-mcp-client explores only the workbench's MCP interface.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func run() error {
	endpoint := flag.String("url", "http://127.0.0.1:8090/mcp", "loopback MCP endpoint")
	tool := flag.String("tool", "", "tool name; omit to list tools")
	args := flag.String("args", "{}", "JSON tool arguments")
	transcript := flag.String("transcript", "", "optional append-only JSONL protocol exploration transcript")
	flag.Parse()
	u, err := url.Parse(*endpoint)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(u.Hostname()); u.Scheme != "http" || ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("MCP client requires an http loopback IP URL")
	}
	var arguments map[string]any
	if err := json.Unmarshal([]byte(*args), &arguments); err != nil || arguments == nil {
		return fmt.Errorf("arguments must be a JSON object: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "go-review-mcp-client", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: *endpoint}, nil)
	if err != nil {
		return err
	}
	defer session.Close()
	var result any
	var toolError bool
	if *tool == "" {
		result, err = session.ListTools(ctx, nil)
	} else {
		var response *mcp.CallToolResult
		response, err = session.CallTool(ctx, &mcp.CallToolParams{Name: *tool, Arguments: arguments})
		if err == nil {
			toolError = response.IsError
			result = response.StructuredContent
			if toolError || result == nil {
				result = response
			}
		}
	}
	if *transcript != "" {
		record := struct {
			Time   time.Time      `json:"time"`
			Tool   string         `json:"tool"`
			Args   map[string]any `json:"args"`
			Result any            `json:"result"`
			Error  string         `json:"error,omitempty"`
		}{Time: time.Now().UTC(), Tool: *tool, Args: arguments, Result: result}
		if err != nil {
			record.Error = err.Error()
		}
		file, e := os.OpenFile(*transcript, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if e != nil {
			return fmt.Errorf("open transcript: %w", e)
		}
		e = json.NewEncoder(file).Encode(record)
		closeErr := file.Close()
		if e != nil {
			return fmt.Errorf("write transcript: %w", e)
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return err
	}
	if toolError {
		return fmt.Errorf("MCP tool reported an error (see response)")
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
