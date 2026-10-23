// Package cli implements the evaluation command for Cuttle.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/a1knowhow/cuttle"
	"github.com/spf13/cobra"
)

func output(cmd *cobra.Command, v any) error { return json.NewEncoder(cmd.OutOrStdout()).Encode(v) }

// NewCommand returns a fresh command tree. Set its input/output streams before execution.
func NewCommand() *cobra.Command {
	var directory string
	root := &cobra.Command{Use: "cuttle", Short: "Embedded versioned knowledge graphs", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().StringVar(&directory, "dir", ".cuttle", "Graph storage directory")
	withStore := func(run func(*cobra.Command, *cuttle.Store, []string) error) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, args []string) error {
			s, e := cuttle.Open(directory)
			if e != nil {
				return e
			}
			defer s.Close()
			return run(cmd, s, args)
		}
	}
	graph := &cobra.Command{Use: "graph", Short: "Manage graphs"}
	graph.AddCommand(&cobra.Command{Use: "create GRAPH", Args: cobra.ExactArgs(1), RunE: withStore(func(cmd *cobra.Command, s *cuttle.Store, args []string) error {
		if e := s.CreateGraph(cmd.Context(), cuttle.GraphID(args[0])); e != nil {
			return e
		}
		return output(cmd, map[string]any{"graph": args[0], "created": true})
	})})
	graph.AddCommand(&cobra.Command{Use: "list", Args: cobra.NoArgs, RunE: withStore(func(cmd *cobra.Command, s *cuttle.Store, _ []string) error {
		ids, e := s.Graphs(cmd.Context())
		if e != nil {
			return e
		}
		return output(cmd, map[string]any{"graphs": ids})
	})})
	graph.AddCommand(&cobra.Command{Use: "delete GRAPH", Short: "Permanently delete a whole graph and its history", Args: cobra.ExactArgs(1), RunE: withStore(func(cmd *cobra.Command, s *cuttle.Store, args []string) error {
		if e := s.DeleteGraph(cmd.Context(), cuttle.GraphID(args[0])); e != nil {
			return e
		}
		return output(cmd, map[string]any{"graph": args[0], "deleted": true})
	})})
	root.AddCommand(graph)
	var importFile string
	imp := &cobra.Command{Use: "import GRAPH", Short: "Apply one atomic versioned batch from a file or standard input", Args: cobra.ExactArgs(1)}
	imp.Flags().StringVar(&importFile, "file", "-", "Batch file; - reads standard input")
	imp.RunE = withStore(func(cmd *cobra.Command, s *cuttle.Store, args []string) error {
		reader := cmd.InOrStdin()
		if importFile != "-" {
			f, e := os.Open(importFile)
			if e != nil {
				return e
			}
			defer f.Close()
			reader = f
		}
		data, e := io.ReadAll(io.LimitReader(reader, (64<<20)+1))
		if e != nil {
			return e
		}
		if len(data) > 64<<20 {
			return cuttle.ErrCapacity
		}
		var batch cuttle.Batch
		if e = decode(data, &batch); e != nil {
			return fmt.Errorf("%w: batch: %v", cuttle.ErrInvalid, e)
		}
		token, e := s.Apply(cmd.Context(), cuttle.GraphID(args[0]), batch)
		if e != nil {
			return e
		}
		return output(cmd, map[string]any{"graph": args[0], "token": token})
	})
	root.AddCommand(imp)
	var graphs []string
	var paramsJSON, validAt, knownAt string
	var token uint64
	query := &cobra.Command{Use: "query CYPHER", Short: "Read selected graphs using parameterized Cypher", Args: cobra.ExactArgs(1)}
	query.Flags().StringSliceVar(&graphs, "graphs", nil, "Explicit graph identifiers, comma-separated")
	query.Flags().StringVar(&paramsJSON, "params", "{}", "Parameter object as JSON (JavaScript Object Notation)")
	query.Flags().StringVar(&validAt, "valid-at", "", "Valid instant, for example 2026-10-22T12:00:00Z")
	query.Flags().StringVar(&knownAt, "known-at", "", "Recorded instant, for example 2026-10-22T12:00:00Z")
	query.Flags().Uint64Var(&token, "token", 0, "Recorded commit token; mutually exclusive with --known-at")
	query.RunE = withStore(func(cmd *cobra.Command, s *cuttle.Store, args []string) error {
		basis := cuttle.Basis{Token: cuttle.Revision(token)}
		var e error
		if validAt != "" {
			basis.ValidAt, e = time.Parse(time.RFC3339Nano, validAt)
			if e != nil {
				return fmt.Errorf("%w: --valid-at: %v", cuttle.ErrInvalid, e)
			}
		}
		if knownAt != "" {
			basis.KnownAt, e = time.Parse(time.RFC3339Nano, knownAt)
			if e != nil {
				return fmt.Errorf("%w: --known-at: %v", cuttle.ErrInvalid, e)
			}
		}
		if cmd.Flags().Changed("token") && (token == 0 || knownAt != "") {
			return fmt.Errorf("%w: --token must be positive and cannot accompany --known-at", cuttle.ErrInvalid)
		}
		var params cuttle.Properties
		if e = decode([]byte(paramsJSON), &params); e != nil || params == nil {
			return fmt.Errorf("%w: --params must be a JSON object", cuttle.ErrInvalid)
		}
		ids := make([]cuttle.GraphID, len(graphs))
		for i, g := range graphs {
			ids[i] = cuttle.GraphID(g)
		}
		view, e := s.View(cmd.Context(), ids, basis)
		if e != nil {
			return e
		}
		result, e := view.Cypher(cmd.Context(), args[0], params)
		if e != nil {
			return e
		}
		return output(cmd, result)
	})
	root.AddCommand(query)
	root.AddCommand(&cobra.Command{Use: "history GRAPH [ASSERTION]", Short: "Inspect recorded versions and reasons", Args: cobra.RangeArgs(1, 2), RunE: withStore(func(cmd *cobra.Command, s *cuttle.Store, args []string) error {
		var id cuttle.ID
		if len(args) == 2 {
			id = cuttle.ID(args[1])
		}
		versions, e := s.History(cmd.Context(), cuttle.GraphID(args[0]), id)
		if e != nil {
			return e
		}
		return output(cmd, map[string]any{"graph": args[0], "history": versions})
	})})
	root.AddCommand(&cobra.Command{Use: "capabilities", Args: cobra.NoArgs, RunE: withStore(func(cmd *cobra.Command, s *cuttle.Store, _ []string) error { return output(cmd, s.Capabilities()) })})
	return root
}

func decode(data []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	d.DisallowUnknownFields()
	if e := d.Decode(target); e != nil {
		return e
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return errors.New("expected exactly one JSON value")
	}
	return nil
}

// Execute writes structured errors and returns a process exit code.
func Execute(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) int {
	command := NewCommand()
	command.SetArgs(args)
	command.SetIn(in)
	command.SetOut(out)
	command.SetErr(errOut)
	if e := command.ExecuteContext(ctx); e != nil {
		json.NewEncoder(errOut).Encode(map[string]any{"error": e.Error(), "code": errorCode(e)})
		return 1
	}
	return 0
}
func errorCode(e error) string {
	for _, v := range []struct {
		err  error
		code string
	}{{cuttle.ErrClosed, "closed"}, {cuttle.ErrNotFound, "not_found"}, {cuttle.ErrExists, "exists"}, {cuttle.ErrConflict, "conflict"}, {cuttle.ErrInvalid, "invalid"}, {cuttle.ErrUnsupported, "unsupported"}, {cuttle.ErrCapacity, "capacity"}, {cuttle.ErrStranded, "stranded_relationship"}, {cuttle.ErrCanceled, "canceled"}} {
		if errors.Is(e, v.err) {
			return v.code
		}
	}
	return "command_failed"
}
