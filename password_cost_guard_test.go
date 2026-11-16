package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// passwordCostViolations reads Go sources (name -> text) the way they ship and
// returns every way they could lower the password cost: a declaration that is
// not exactly `var passwordCost = bcrypt.DefaultCost`, a second declaration,
// any assignment to it, its address taken, or it being incremented. With no
// write anywhere in the shipped files, no environment variable, flag or file
// can reach it. It returns an error only when it could not look.
func passwordCostViolations(sources map[string]string) ([]string, error) {
	var bad []string
	decls := 0
	fset := token.NewFileSet()
	for name, src := range sources {
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			return nil, err
		}
		isCost := func(e ast.Expr) bool {
			id, ok := e.(*ast.Ident)
			return ok && id.Name == "passwordCost"
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.ValueSpec:
				for i, id := range v.Names {
					if id.Name != "passwordCost" {
						continue
					}
					decls++
					ok := false
					if len(v.Values) == len(v.Names) {
						if sel, isSel := v.Values[i].(*ast.SelectorExpr); isSel {
							pkg, isID := sel.X.(*ast.Ident)
							ok = isID && pkg.Name == "bcrypt" && sel.Sel.Name == "DefaultCost"
						}
					}
					if !ok {
						bad = append(bad, name+": passwordCost is not declared as bcrypt.DefaultCost")
					}
				}
			case *ast.AssignStmt:
				for _, lhs := range v.Lhs {
					if isCost(lhs) {
						bad = append(bad, name+": passwordCost is assigned")
					}
				}
			case *ast.IncDecStmt:
				if isCost(v.X) {
					bad = append(bad, name+": passwordCost is incremented or decremented")
				}
			case *ast.UnaryExpr:
				if v.Op == token.AND && isCost(v.X) {
					bad = append(bad, name+": the address of passwordCost is taken")
				}
			}
			return true
		})
	}
	if decls != 1 {
		bad = append(bad, "passwordCost must be declared exactly once in the shipped files")
	}
	return bad, nil
}

// shippedSources is every Go file the production binary is built from.
func shippedSources(t *testing.T) map[string]string {
	t.Helper()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("list sources: %v", err)
	}
	out := map[string]string{}
	for _, n := range names {
		if strings.HasSuffix(n, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(n)
		if err != nil {
			t.Fatalf("read %s: %v", n, err)
		}
		out[n] = string(raw)
	}
	if len(out) == 0 {
		t.Fatal("no shipped Go source was found, so nothing was checked")
	}
	return out
}

// The shipped binary hashes at the production cost and has no path to lower it.
func TestShippedBinaryCannotLowerThePasswordCost(t *testing.T) {
	bad, err := passwordCostViolations(shippedSources(t))
	if err != nil {
		t.Fatalf("could not read the shipped sources: %v", err)
	}
	for _, b := range bad {
		t.Error(b)
	}
}

// The guard fires: each way of lowering the cost is caught, and the honest
// declaration alone is not.
func TestPasswordCostGuardCatchesEachWayOfLoweringIt(t *testing.T) {
	const head = "package main\nimport (\"os\"; \"strconv\"; \"golang.org/x/crypto/bcrypt\")\nvar _, _ = os.Getenv, strconv.Atoi\n"
	const honest = head + "var passwordCost = bcrypt.DefaultCost\n"
	cases := map[string]string{
		"an environment variable": honest + "func init() { if n, err := strconv.Atoi(os.Getenv(\"PASSWORD_COST\")); err == nil { passwordCost = n } }\n",
		"a literal":               head + "var passwordCost = 4\n",
		"the minimum cost":        head + "var passwordCost = bcrypt.MinCost\n",
		"a pointer":               honest + "func lower() { p := &passwordCost; *p = 4 }\n",
		"a decrement":             honest + "func lower() { passwordCost-- }\n",
		"a second declaration":    honest + "func lower() { var passwordCost = bcrypt.DefaultCost; _ = passwordCost }\n",
		"no declaration":          head,
	}
	for name, src := range cases {
		bad, err := passwordCostViolations(map[string]string{"x.go": src})
		if err != nil {
			t.Fatalf("%s: the fixture does not parse: %v", name, err)
		}
		if len(bad) == 0 {
			t.Errorf("%s: the guard reported nothing", name)
		}
	}
	bad, err := passwordCostViolations(map[string]string{"x.go": honest})
	if err != nil || len(bad) != 0 {
		t.Errorf("the honest declaration was refused: %v %v", bad, err)
	}
}

// A hash made at the production setting carries bcrypt's default cost, and the
// test binary's own hashes carry the minimum: both halves of the arrangement.
func TestPasswordCostIsDefaultInProductionAndMinimumUnderTest(t *testing.T) {
	under, err := HashPassword("long-enough-pw-1")
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := bcrypt.Cost([]byte(under)); c != bcrypt.MinCost {
		t.Errorf("a test hash has cost %d, want the minimum %d", c, bcrypt.MinCost)
	}
	was := passwordCost
	passwordCost = bcrypt.DefaultCost
	defer func() { passwordCost = was }()
	prod, err := HashPassword("long-enough-pw-1")
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := bcrypt.Cost([]byte(prod)); c != bcrypt.DefaultCost {
		t.Errorf("a production hash has cost %d, want %d", c, bcrypt.DefaultCost)
	}
}

func TestHealthzNamesTheBuildCommitWhenThereIsOne(t *testing.T) {
	was := buildCommit
	defer func() { buildCommit = was }()
	buildCommit = ""
	if got := string(healthzBody()); got != `{"status":"ok"}` {
		t.Errorf("with no commit /healthz answers %s", got)
	}
	buildCommit = "0123abc"
	if got := string(healthzBody()); got != `{"commit":"0123abc","status":"ok"}` {
		t.Errorf("with a commit /healthz answers %s", got)
	}
}
