// Command list shows crossmem used as a library: the recent sessions for a
// folder across every agent tool, and the opening turns of the newest one.
//
//	go run ./examples/list [folder]
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/muthuishere/crossmemcli/pkg/crossmem"
)

func main() {
	folder := "."
	if len(os.Args) > 1 {
		folder = os.Args[1]
	}
	client, err := crossmem.New(crossmem.Options{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "crossmem:", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sessions, err := client.List(ctx, crossmem.ListOptions{CWD: folder, Limit: 5, Questions: true})
	if err != nil {
		fmt.Fprintln(os.Stderr, "crossmem:", err)
		os.Exit(1)
	}
	for _, s := range sessions {
		fmt.Printf("%-12s %-14s %s\n  ref: %s\n", s.Provider, s.Ago, s.Title, s.Ref)
		for _, child := range s.Children {
			fmt.Printf("  subagent: %s\n", child)
		}
	}
	if len(sessions) == 0 {
		return
	}
	tr, err := client.Transcript(ctx, sessions[0].Ref)
	if err != nil {
		fmt.Fprintln(os.Stderr, "crossmem:", err)
		os.Exit(1)
	}
	fmt.Printf("\nnewest session: %d events\n", len(tr.Events))
	for i, e := range tr.Events {
		if i == 4 {
			break
		}
		content := e.Content
		if len(content) > 100 {
			content = content[:100] + "…"
		}
		fmt.Printf("  %-9s %s\n", e.Role, content)
	}
}
