package vcl

import (
	"regexp"
	"strconv"
	"strings"
)

var blockedSplit = []string{"dex", "tex", "lex", "rex", "sex"}

// splitOldField separates glued trailing field letters (old syntax).
func splitOldField(text string) (string, uint8, bool) {
	end := len(text)
	i := end
	for i > 0 && strings.IndexByte("xyzw", text[i-1]) >= 0 {
		i--
	}
	run := text[i:]
	if run == "" || i == 0 {
		return text, 0, false
	}
	m, ok := parseFields(run)
	if !ok {
		return text, 0, false
	}
	base := text[:i]
	if len(run) >= 2 {
		return base, m, true
	}
	prev := base[len(base)-1]
	isLetter := prev >= 'a' && prev <= 'z' || prev >= 'A' && prev <= 'Z'
	if !isLetter {
		return base, m, true
	}
	if len(base) < 2 {
		return text, 0, false
	}
	for _, b := range blockedSplit {
		if strings.HasSuffix(text, b) {
			return text, 0, false
		}
	}
	return base, m, true
}

// splitNewField separates a bracketed field selection (new syntax).
func splitNewField(text string) (string, uint8, bool, bool) {
	if !strings.HasSuffix(text, "]") {
		return text, 0, false, true
	}
	k := strings.LastIndexByte(text, '[')
	if k < 0 {
		return text, 0, false, true
	}
	m, ok := parseFields(text[k+1 : len(text)-1])
	if !ok || m == 0 {
		return text, 0, false, true
	}
	return text[:k], m, true, true
}

func (p *parser) regName(text string, isFloat bool, allowField bool) (op *operand, ok bool) {
	op = &operand{text: text, phys: -1}
	base := text
	var fld uint8
	has := false
	if allowField {
		if p.syntax {
			if b, m, h, _ := splitNewField(text); h {
				base, fld, has = b, m, true
			}
		} else {
			if b, m, h := splitOldField(text); h {
				base, fld, has = b, m, true
			}
		}
	}
	if isFloat {
		if m := physVF.FindStringSubmatch(base); m != nil {
			n, _ := strconv.Atoi(m[1])
			if n > 31 {
				return nil, false
			}
			op.phys = n
			op.lit = true
			op.name = ""
			op.fields, op.hasFld = fld, has
			return op, true
		}
	} else {
		if m := physVI.FindStringSubmatch(base); m != nil {
			n, _ := strconv.Atoi(m[1])
			if n > 15 {
				return nil, false
			}
			op.phys = n
			op.lit = true
			op.fields, op.hasFld = fld, has
			return op, true
		}
	}
	if !validAlias(base) {
		// a split that left an invalid remainder keeps the whole text as one alias
		if has && validAlias(text) {
			op.name = text
			return op, true
		}
		return nil, false
	}
	op.name = base
	op.fields, op.hasFld = fld, has
	return op, true
}

var floatSpecial = map[string]argKind{"acc": kACC, "q": kQ, "p": kP, "i": kI, "r": kR}

var memRe = regexp.MustCompile(`^(.*)\(\s*([^()]*?)\s*\)$`)

// evalImmediate evaluates an arithmetic operand for an integer slot.
func evalImmediate(text string) (string, bool, bool) {
	r := evalExpr(text)
	if r.parenFn {
		return "", false, true
	}
	if !r.ok {
		return text, true, false
	}
	return strconv.Itoa(int(truncInt32(r.value))), true, false
}

func hex8(v uint32) string {
	const digits = "0123456789abcdef"
	b := make([]byte, 10)
	b[0], b[1] = '0', 'x'
	for i := 0; i < 8; i++ {
		b[9-i] = digits[(v>>(4*uint(i)))&0xf]
	}
	return string(b)
}

func evalLoi(text string) string {
	r := evalExpr(text)
	if !r.ok {
		return text
	}
	return hex8(floatBits(r.value))
}
