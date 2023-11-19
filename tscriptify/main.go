package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"unicode"
)

type arrayImports []string

func (i *arrayImports) String() string {
	return "// custom imports:\n\n" + strings.Join(*i, "\n")
}

func (i *arrayImports) Set(value string) error {
	*i = append(*i, value)
	return nil
}

const TEMPLATE = `package main

import (
	{{ .Models }}
	"github.com/tkrajina/typescriptify-golang-structs/typescriptify"
{{ .ExtraImports }}
)

func main() {
	t := typescriptify.New()
	t.CreateInterface = {{ .Interface }}
{{ range $key, $value := .InitParams }}	t.{{ $key }}={{ $value }}
{{ end }}
{{ range .Structs }}	t.Add({{ . }}{})
{{ end }}
{{ range .CustomImports }}	t.AddImport("{{ . }}")
{{ end }}
	{{ .ExtraCommands }}
	err := t.ConvertToFile("{{ .TargetFile }}")
	if err != nil {
		panic(err.Error())
	}
}

type enum[T any] struct {
	Value  T
	TSName string
}

func stringEnum[T ~string](xs []T) []enum[T] {
	out := make([]enum[T], 0, len(xs))
	for _, x := range xs {
		out = append(out, enum[T]{
			Value: x,
			TSName: string(x),
		})
	}
	return out
}`

type Params struct {
	ModelsPackage string
	TargetFile    string
	Structs       []string
	InitParams    map[string]interface{}
	CustomImports arrayImports
	Interface     bool
	Verbose       bool
	ExtraImports  string
	ExtraCommands string

	Models string
}

func main() {
	var p Params
	var backupDir string
	flag.StringVar(&p.ModelsPackage, "package", "", "Path of the package with models")
	flag.StringVar(&p.TargetFile, "target", "", "Target typescript file")
	flag.StringVar(&p.ExtraImports, "extra-imports", "", "Filename containing extra imports to include in the generated file")
	flag.StringVar(&p.ExtraCommands, "extra-commands", "", "Filename containing extra content to include in the generated file")
	flag.StringVar(&backupDir, "backup", "", "Directory where backup files are saved")
	flag.BoolVar(&p.Interface, "interface", false, "Create interfaces (not classes)")
	flag.Var(&p.CustomImports, "import", "Typescript import for your custom type, repeat this option for each import needed")
	flag.BoolVar(&p.Verbose, "verbose", false, "Verbose logs")
	flag.Parse()

	structs := []string{}
	paths := map[string]int{}
	for _, structOrGoFile := range flag.Args() {
		if !strings.HasSuffix(structOrGoFile, ".go") {
			structs = append(structs, structOrGoFile)
			continue
		}
		path := filepath.Dir(filepath.Join(p.ModelsPackage, structOrGoFile))
		n, exist := paths[path]
		if !exist {
			paths[path] = len(paths)
			n = paths[path]
		}
		fileStructs, err := GetGolangFileStructs(structOrGoFile)
		if err != nil {
			panic(fmt.Sprintf("Error loading/parsing golang file %s: %s", structOrGoFile, err.Error()))
		}
		for _, s := range fileStructs {
			structs = append(structs, fmt.Sprintf("m%d.%s", n, s))
		}
	}

	if len(p.ModelsPackage) == 0 {
		fmt.Fprintln(os.Stderr, "No package given")
		os.Exit(1)
	}
	if len(p.TargetFile) == 0 {
		fmt.Fprintln(os.Stderr, "No target file")
		os.Exit(1)
	}

	t := template.Must(template.New("").Parse(TEMPLATE))

	f, err := os.CreateTemp(os.TempDir(), "typescriptify_*.go")
	handleErr(err)
	defer f.Close()

	structsArr := make([]string, 0)
	for _, str := range structs {
		str = strings.TrimSpace(str)
		if strings.HasPrefix(str, ".") {
			continue
		}
		if strings.Contains(str, string(filepath.Separator)) {
			continue
		}
		if len(str) > 0 {
			structsArr = append(structsArr, str)
		}
	}

	p.Structs = structsArr
	p.InitParams = map[string]interface{}{
		"BackupDir": fmt.Sprintf(`"%s"`, backupDir),
	}

	if p.ExtraImports != "" {
		byt, err := os.ReadFile(p.ExtraImports)
		handleErr(err)
		p.ExtraImports = string(byt)
	}
	if p.ExtraCommands != "" {
		byt, err := os.ReadFile(p.ExtraCommands)
		handleErr(err)
		p.ExtraCommands = string(byt)
	}

	models := make([]string, 0, len(paths))
	for k, v := range paths {
		models = append(models, fmt.Sprintf("m%d %q", v, k))
	}
	p.Models = strings.Join(models, "\n\t")
	err = t.Execute(f, p)
	handleErr(err)

	if p.Verbose {
		byts, err := os.ReadFile(f.Name())
		handleErr(err)
		fmt.Printf("\nCompiling generated code (%s):\n%s\n----------------------------------------------------------------------------------------------------\n", f.Name(), string(byts))
	}

	cmd := exec.Command("go", "run", f.Name())
	output, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Println(string(output))
		handleErr(err)
	}
}

func GetGolangFileStructs(filename string) ([]string, error) {
	fset := token.NewFileSet() // positions are relative to fset

	f, err := parser.ParseFile(fset, filename, nil, 0)
	if err != nil {
		return nil, err
	}

	v := &AVisitor{}
	ast.Walk(v, f)

	return v.structs, nil
}

type AVisitor struct {
	structNameCandidate string
	structs             []string
}

func (v *AVisitor) Visit(node ast.Node) ast.Visitor {
	if node != nil {
		switch t := node.(type) {
		case *ast.Ident:
			if unicode.IsUpper(rune(t.Name[0])) {
				v.structNameCandidate = t.Name
			} else {
				v.structNameCandidate = ""
			}
		case *ast.StructType:
			if len(v.structNameCandidate) > 0 {
				if unicode.IsUpper(rune(v.structNameCandidate[0])) {
					v.structs = append(v.structs, v.structNameCandidate)
					v.structNameCandidate = ""
				} else {
					v.structNameCandidate = ""
				}
			}
		default:
			v.structNameCandidate = ""
		}
	}
	return v
}

func handleErr(err error) {
	if err != nil {
		panic(err.Error())
	}
}
