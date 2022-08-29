package rules

import (
	"bufio"
	"errors"
	"fmt"
	"go/types"
	"io"
	"sort"
	"strings"

	"github.com/timonwong/loggercheck/internal/bytebufferpool"
)

var ErrInvalidRule = errors.New("invalid rule format")

type RulesetList []Ruleset

func (rl RulesetList) HasName(name string) bool {
	for _, rs := range rl {
		if rs.Name == name {
			return true
		}
	}
	return false
}

func (rl RulesetList) Names() []string {
	keys := make([]string, len(rl))
	visited := make(map[string]struct{})
	for i, pg := range rl {
		if _, ok := visited[pg.Name]; ok {
			continue
		}
		visited[pg.Name] = struct{}{}
		keys[i] = pg.Name
	}
	sort.Strings(keys)
	return keys
}

type Ruleset struct {
	Name          string
	PackageImport string
	Rules         []FuncRule
}

func (rs *Ruleset) Match(fn *types.Func, pkg *types.Package) bool {
	pkgPath := pkg.Path()
	if pkgPath != rs.PackageImport && !strings.HasSuffix(pkgPath, "/vendor/"+rs.PackageImport) {
		return false
	}

	sig := fn.Type().(*types.Signature) // it's safe since we already checked
	for _, rule := range rs.Rules {
		if rule.match(fn, sig) {
			return true
		}
	}

	return false
}

func emptyQualifier(*types.Package) string { return "" }

type FuncRule struct { // package import should be accessed from Rulset
	ReceiverType string
	MethodName   string
	IsReceiver   bool
}

func (p *FuncRule) match(fn *types.Func, sig *types.Signature) bool {
	// we do not check package import here since it's already checked
	if fn.Name() != p.MethodName {
		return false
	}

	recv := sig.Recv()
	isReceiver := recv != nil
	if isReceiver != p.IsReceiver {
		return false
	}

	if isReceiver {
		recvType := recv.Type()
		recvTypeBuf := bytebufferpool.Get()
		defer bytebufferpool.Put(recvTypeBuf)
		types.WriteType(recvTypeBuf, recvType, emptyQualifier)
		if recvTypeBuf.String() != p.ReceiverType {
			return false
		}
	}

	return true
}

func ParseFuncRule(rule string) (packageImport string, pat FuncRule, err error) {
	lastDot := strings.LastIndexFunc(rule, func(r rune) bool {
		return r == '.' || r == '/'
	})
	if lastDot == -1 || rule[lastDot] == '/' {
		return "", pat, ErrInvalidRule
	}

	importOrReceiver := rule[:lastDot]
	pat.MethodName = rule[lastDot+1:]

	if strings.HasPrefix(rule, "(") { // package
		if !strings.HasSuffix(importOrReceiver, ")") {
			return "", FuncRule{}, ErrInvalidRule
		}

		var isPointerReceiver bool
		pat.IsReceiver = true
		receiver := importOrReceiver[1 : len(importOrReceiver)-1]
		if strings.HasPrefix(receiver, "*") {
			isPointerReceiver = true
			receiver = receiver[1:]
		}

		typeDotIdx := strings.LastIndexFunc(receiver, func(r rune) bool {
			return r == '.' || r == '/'
		})
		if typeDotIdx == -1 || receiver[typeDotIdx] == '/' {
			return "", FuncRule{}, ErrInvalidRule
		}
		receiverType := receiver[typeDotIdx+1:]
		if isPointerReceiver {
			receiverType = "*" + receiverType
		}
		pat.ReceiverType = receiverType
		packageImport = receiver[:typeDotIdx]
	} else {
		packageImport = importOrReceiver
	}

	return packageImport, pat, nil
}

func ParseRules(lines []string) (result RulesetList, err error) {
	rulesByImport := make(map[string][]FuncRule)
	for i, line := range lines {
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "#") { // comments
			continue
		}

		packageImport, pat, err := ParseFuncRule(line)
		if err != nil {
			return nil, fmt.Errorf("error parse rule at line %d: %w", i+1, err)
		}
		rulesByImport[packageImport] = append(rulesByImport[packageImport], pat)
	}

	for packageImport, rules := range rulesByImport {
		result = append(result, Ruleset{
			Name:          "", // NOTE(timonwong) Always "" for custom rule
			PackageImport: packageImport,
			Rules:         rules,
		})
	}
	return result, nil
}

func ParseRuleFile(r io.Reader) (result RulesetList, err error) {
	// Rule files are relatively small, so read it into string slice first.
	var lines []string

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return ParseRules(lines)
}
