package review

import (
	"context"
	"fmt"
	"go/types"
	"strings"

	"golang.org/x/tools/go/ssa"
)

// DataFlowEvidence distinguishes legacy absence from collected-but-unavailable
// symbols. Each package is built once, using the original review's type objects.
type DataFlowEvidence struct {
	SourceSHA256 string           `json:"sourceSHA256"`
	Commands     []Command        `json:"commands"`
	Results      []DataFlowResult `json:"results"`
}

type DataFlowResult struct {
	SymbolID string    `json:"symbolID"`
	Status   string    `json:"status"`
	Error    string    `json:"error,omitempty"`
	Graph    *DataFlow `json:"graph,omitempty"`
}

func buildDataPackage(a *analyzer, p *parsedPackage) (prog *ssa.Program, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			prog = nil
			err = fmt.Errorf("SSA package build unavailable: %v", recovered)
		}
	}()
	prog = ssa.NewProgram(a.fset, ssa.SanityCheckFunctions)
	var dependencies func(*types.Package)
	dependencies = func(pkg *types.Package) {
		if prog.Package(pkg) != nil {
			return
		}
		for _, dep := range pkg.Imports() {
			dependencies(dep)
		}
		prog.CreatePackage(pkg, nil, nil, true)
	}
	for _, dep := range p.pkg.Imports() {
		dependencies(dep)
	}
	pkg := prog.CreatePackage(p.pkg, p.ast, p.info, true)
	pkg.Build()
	return prog, nil
}

func (a *analyzer) collectDataFlows(ctx context.Context, paths []string) error {
	r := a.report
	r.DataFlows = &DataFlowEvidence{SourceSHA256: r.SourceSHA256, Commands: r.Commands, Results: []DataFlowResult{}}
	version := dataToolVersion()
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		var symbols []*Symbol
		for i := range r.Symbols {
			s := &r.Symbols[i]
			if s.Package == path && (s.Kind == "function" || s.Kind == "method" || s.Kind == "closure") {
				symbols = append(symbols, s)
			}
		}
		if len(symbols) == 0 {
			continue
		}
		p := a.packages[path]
		var packageErr error
		for _, pack := range r.Packages {
			if pack.Path == path && len(pack.TypeErrors) > 0 {
				packageErr = fmt.Errorf("type checking unavailable: %s", strings.Join(pack.TypeErrors, "; "))
			}
		}
		if len(p.meta.CgoFiles) > 0 {
			packageErr = fmt.Errorf("unsupported cgo package without generated bindings")
		}
		var prog *ssa.Program
		if packageErr == nil {
			prog, packageErr = buildDataPackage(a, p)
		}
		for _, s := range symbols {
			if err := ctx.Err(); err != nil {
				return err
			}
			result := DataFlowResult{SymbolID: s.ID, Status: "error"}
			err := packageErr
			if err == nil {
				var fn *ssa.Function
				fn, err = buildDataFunction(a, p, s, prog)
				if err != nil {
					result.Status = "unsupported"
				} else {
					result.Graph, err = collectDataFlow(ctx, a, fn, s, r)
				}
			}
			if err != nil {
				result.Graph = nil
				result.Error = err.Error()
			} else {
				result.Status = "available"
				result.Graph.Collector = "golang.org/x/tools/go/ssa"
				result.Graph.ToolVersion = version
				result.Graph.GeneratedAt = r.GeneratedAt
			}
			r.DataFlows.Results = append(r.DataFlows.Results, result)
		}
	}
	return nil
}
