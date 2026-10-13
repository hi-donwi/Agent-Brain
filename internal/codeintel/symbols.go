package codeintel

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"path/filepath"
	"regexp"
	"strings"
)

// SymbolKind categorizes a code symbol.
type SymbolKind string

const (
	KindFunction  SymbolKind = "function"
	KindMethod    SymbolKind = "method"
	KindType      SymbolKind = "type"
	KindInterface SymbolKind = "interface"
	KindStruct    SymbolKind = "struct"
	KindClass     SymbolKind = "class"
	KindVariable  SymbolKind = "variable"
	KindConstant  SymbolKind = "constant"
)

// Symbol represents an extracted code definition with location and signature.
type Symbol struct {
	Name      string     `json:"name"`
	Kind      SymbolKind `json:"kind"`
	Path      string     `json:"path"`
	Line      int        `json:"line"`
	EndLine   int        `json:"end_line"`
	Signature string     `json:"signature"`
	Doc       string     `json:"doc,omitempty"`
}

// ExtractSymbols extracts declarations from source code based on file extension.
func ExtractSymbols(relPath string, content []byte) ([]Symbol, error) {
	ext := strings.ToLower(filepath.Ext(relPath))
	switch ext {
	case ".go":
		return extractGoSymbols(relPath, content)
	case ".py":
		return extractPythonSymbols(relPath, content)
	case ".ts", ".tsx", ".js", ".jsx":
		return extractJSTSSymbols(relPath, content)
	case ".rs":
		return extractRustSymbols(relPath, content)
	default:
		return nil, nil
	}
}

// extractGoSymbols uses Go's standard library parser and AST (pure Go, zero cgo).
func extractGoSymbols(relPath string, content []byte) ([]Symbol, error) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, relPath, content, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse go file %s: %w", relPath, err)
	}

	var symbols []Symbol

	for _, decl := range node.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			kind := KindFunction
			var sig strings.Builder

			if d.Recv != nil && len(d.Recv.List) > 0 {
				kind = KindMethod
				sig.WriteString("func (")
				for i, field := range d.Recv.List {
					if i > 0 {
						sig.WriteString(", ")
					}
					var typeBuf bytes.Buffer
					_ = printer.Fprint(&typeBuf, fset, field.Type)
					if len(field.Names) > 0 {
						sig.WriteString(field.Names[0].Name + " ")
					}
					sig.WriteString(typeBuf.String())
				}
				sig.WriteString(") ")
			} else {
				sig.WriteString("func ")
			}

			sig.WriteString(d.Name.Name)
			if d.Type.Params != nil {
				var paramsBuf bytes.Buffer
				_ = printer.Fprint(&paramsBuf, fset, d.Type.Params)
				sig.WriteString(paramsBuf.String())
			}
			if d.Type.Results != nil {
				var resultsBuf bytes.Buffer
				_ = printer.Fprint(&resultsBuf, fset, d.Type.Results)
				sig.WriteString(" " + resultsBuf.String())
			}

			startPos := fset.Position(d.Pos())
			endPos := fset.Position(d.End())

			doc := ""
			if d.Doc != nil {
				doc = strings.TrimSpace(d.Doc.Text())
			}

			symbols = append(symbols, Symbol{
				Name:      d.Name.Name,
				Kind:      kind,
				Path:      relPath,
				Line:      startPos.Line,
				EndLine:   endPos.Line,
				Signature: sig.String(),
				Doc:       doc,
			})

		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					kind := KindType
					switch s.Type.(type) {
					case *ast.StructType:
						kind = KindStruct
					case *ast.InterfaceType:
						kind = KindInterface
					}

					var typeBuf bytes.Buffer
					_ = printer.Fprint(&typeBuf, fset, s.Type)

					startPos := fset.Position(s.Pos())
					endPos := fset.Position(s.End())

					doc := ""
					if s.Doc != nil {
						doc = strings.TrimSpace(s.Doc.Text())
					} else if d.Doc != nil {
						doc = strings.TrimSpace(d.Doc.Text())
					}

					symbols = append(symbols, Symbol{
						Name:      s.Name.Name,
						Kind:      kind,
						Path:      relPath,
						Line:      startPos.Line,
						EndLine:   endPos.Line,
						Signature: fmt.Sprintf("type %s %s", s.Name.Name, firstLine(typeBuf.String())),
						Doc:       doc,
					})

				case *ast.ValueSpec:
					kind := KindVariable
					if d.Tok == token.CONST {
						kind = KindConstant
					}
					for _, name := range s.Names {
						startPos := fset.Position(name.Pos())
						endPos := fset.Position(name.End())
						symbols = append(symbols, Symbol{
							Name:      name.Name,
							Kind:      kind,
							Path:      relPath,
							Line:      startPos.Line,
							EndLine:   endPos.Line,
							Signature: fmt.Sprintf("%s %s", d.Tok.String(), name.Name),
						})
					}
				}
			}
		}
	}

	return symbols, nil
}

var (
	pyDefRegex   = regexp.MustCompile(`^(\s*)def\s+([a-zA-Z0-9_]+)\s*\((.*?)\):`)
	pyClassRegex = regexp.MustCompile(`^(\s*)class\s+([a-zA-Z0-9_]+)(\(.*?\))?:`)
)

func extractPythonSymbols(relPath string, content []byte) ([]Symbol, error) {
	lines := strings.Split(string(content), "\n")
	var symbols []Symbol

	for i, line := range lines {
		lineNum := i + 1
		trimmed := strings.TrimRight(line, "\r")

		if matches := pyClassRegex.FindStringSubmatch(trimmed); len(matches) > 2 {
			name := matches[2]
			sig := strings.TrimSpace(trimmed)
			symbols = append(symbols, Symbol{
				Name:      name,
				Kind:      KindClass,
				Path:      relPath,
				Line:      lineNum,
				EndLine:   lineNum,
				Signature: sig,
			})
			continue
		}

		if matches := pyDefRegex.FindStringSubmatch(trimmed); len(matches) > 2 {
			indent := matches[1]
			name := matches[2]
			kind := KindFunction
			if len(indent) > 0 {
				kind = KindMethod
			}
			sig := strings.TrimSpace(trimmed)
			symbols = append(symbols, Symbol{
				Name:      name,
				Kind:      kind,
				Path:      relPath,
				Line:      lineNum,
				EndLine:   lineNum,
				Signature: sig,
			})
		}
	}

	return symbols, nil
}

var (
	tsFuncRegex      = regexp.MustCompile(`^\s*(export\s+)?(async\s+)?function\s+([a-zA-Z0-9_$]+)\s*\((.*?)\)`)
	tsClassRegex     = regexp.MustCompile(`^\s*(export\s+)?(abstract\s+)?class\s+([a-zA-Z0-9_$]+)`)
	tsInterfaceRegex = regexp.MustCompile(`^\s*(export\s+)?interface\s+([a-zA-Z0-9_$]+)`)
	tsTypeRegex      = regexp.MustCompile(`^\s*(export\s+)?type\s+([a-zA-Z0-9_$]+)\s*=`)
)

func extractJSTSSymbols(relPath string, content []byte) ([]Symbol, error) {
	lines := strings.Split(string(content), "\n")
	var symbols []Symbol

	for i, line := range lines {
		lineNum := i + 1
		trimmed := strings.TrimRight(line, "\r")

		if matches := tsClassRegex.FindStringSubmatch(trimmed); len(matches) > 3 {
			symbols = append(symbols, Symbol{
				Name:      matches[3],
				Kind:      KindClass,
				Path:      relPath,
				Line:      lineNum,
				EndLine:   lineNum,
				Signature: strings.TrimSpace(trimmed),
			})
			continue
		}

		if matches := tsInterfaceRegex.FindStringSubmatch(trimmed); len(matches) > 2 {
			symbols = append(symbols, Symbol{
				Name:      matches[2],
				Kind:      KindInterface,
				Path:      relPath,
				Line:      lineNum,
				EndLine:   lineNum,
				Signature: strings.TrimSpace(trimmed),
			})
			continue
		}

		if matches := tsTypeRegex.FindStringSubmatch(trimmed); len(matches) > 2 {
			symbols = append(symbols, Symbol{
				Name:      matches[2],
				Kind:      KindType,
				Path:      relPath,
				Line:      lineNum,
				EndLine:   lineNum,
				Signature: strings.TrimSpace(trimmed),
			})
			continue
		}

		if matches := tsFuncRegex.FindStringSubmatch(trimmed); len(matches) > 3 {
			symbols = append(symbols, Symbol{
				Name:      matches[3],
				Kind:      KindFunction,
				Path:      relPath,
				Line:      lineNum,
				EndLine:   lineNum,
				Signature: strings.TrimSpace(trimmed),
			})
		}
	}

	return symbols, nil
}

var (
	rustFnRegex     = regexp.MustCompile(`^\s*(pub(\(.*?\))?\s+)?(async\s+)?fn\s+([a-zA-Z0-9_]+)`)
	rustStructRegex = regexp.MustCompile(`^\s*(pub(\(.*?\))?\s+)?struct\s+([a-zA-Z0-9_]+)`)
	rustEnumRegex   = regexp.MustCompile(`^\s*(pub(\(.*?\))?\s+)?enum\s+([a-zA-Z0-9_]+)`)
	rustTraitRegex  = regexp.MustCompile(`^\s*(pub(\(.*?\))?\s+)?trait\s+([a-zA-Z0-9_]+)`)
)

func extractRustSymbols(relPath string, content []byte) ([]Symbol, error) {
	lines := strings.Split(string(content), "\n")
	var symbols []Symbol

	for i, line := range lines {
		lineNum := i + 1
		trimmed := strings.TrimRight(line, "\r")

		if matches := rustStructRegex.FindStringSubmatch(trimmed); len(matches) > 3 {
			symbols = append(symbols, Symbol{
				Name:      matches[3],
				Kind:      KindStruct,
				Path:      relPath,
				Line:      lineNum,
				EndLine:   lineNum,
				Signature: strings.TrimSpace(trimmed),
			})
			continue
		}

		if matches := rustEnumRegex.FindStringSubmatch(trimmed); len(matches) > 3 {
			symbols = append(symbols, Symbol{
				Name:      matches[3],
				Kind:      KindType,
				Path:      relPath,
				Line:      lineNum,
				EndLine:   lineNum,
				Signature: strings.TrimSpace(trimmed),
			})
			continue
		}

		if matches := rustTraitRegex.FindStringSubmatch(trimmed); len(matches) > 3 {
			symbols = append(symbols, Symbol{
				Name:      matches[3],
				Kind:      KindInterface,
				Path:      relPath,
				Line:      lineNum,
				EndLine:   lineNum,
				Signature: strings.TrimSpace(trimmed),
			})
			continue
		}

		if matches := rustFnRegex.FindStringSubmatch(trimmed); len(matches) > 4 {
			symbols = append(symbols, Symbol{
				Name:      matches[4],
				Kind:      KindFunction,
				Path:      relPath,
				Line:      lineNum,
				EndLine:   lineNum,
				Signature: strings.TrimSpace(trimmed),
			})
		}
	}

	return symbols, nil
}

func firstLine(s string) string {
	idx := strings.Index(s, "\n")
	if idx != -1 {
		return strings.TrimSpace(s[:idx])
	}
	return strings.TrimSpace(s)
}
