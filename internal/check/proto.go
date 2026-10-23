// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// A small reader of .proto files (messages, enums, services) and the
// protobuf wire format, enough to call one gRPC method with a JSON body and
// read its answer as JSON. Imports are not followed; google.protobuf.Empty
// is known.

type protoField struct {
	name     string
	jsonName string
	number   int
	typ      string // a scalar type, or a message or enum's full name
	repeated bool
	mapKey   string // map fields: the key type; typ is the value type
}

type protoMessage struct {
	name   string
	fields []*protoField
}

type protoEnum struct {
	values map[string]int
	names  map[int]string
}

type protoMethod struct {
	in, out string // full message names
	stream  bool
}

// protoFile is what a .proto file defines, by full name (package.Name).
type protoFile struct {
	messages map[string]*protoMessage
	enums    map[string]*protoEnum
	methods  map[string]protoMethod // "package.Service/Method"
}

var scalars = map[string]bool{"double": true, "float": true, "int32": true, "int64": true, "uint32": true, "uint64": true,
	"sint32": true, "sint64": true, "fixed32": true, "fixed64": true, "sfixed32": true, "sfixed64": true, "bool": true,
	"string": true, "bytes": true}

// protoTokens splits a .proto file into tokens, without comments.
func protoTokens(src string) ([]string, error) {
	var out []string
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case strings.HasPrefix(src[i:], "//"):
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case strings.HasPrefix(src[i:], "/*"):
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				return nil, errors.New("a comment is not closed")
			}
			i += end + 4
		case c == '"' || c == '\'':
			j, err := stringEnd(src, i)
			if err != nil {
				return nil, err
			}
			out = append(out, src[i:j])
			i = j
		case strings.ContainsRune("{}()<>;=,[]", rune(c)):
			out = append(out, string(c))
			i++
		default:
			j := i
			for j < len(src) && !strings.ContainsRune(" \t\r\n{}()<>;=,[]\"'/", rune(src[j])) {
				j++
			}
			if j == i {
				return nil, fmt.Errorf("unexpected %q", c)
			}
			out = append(out, src[i:j])
			i = j
		}
	}
	return out, nil
}

// stringEnd finds the end of the quoted string at i.
func stringEnd(src string, i int) (int, error) {
	q := src[i]
	for j := i + 1; j < len(src); j++ {
		switch src[j] {
		case '\\':
			j++
		case q:
			return j + 1, nil
		}
	}
	return 0, errors.New("a string is not closed")
}

// protoParser reads tokens.
type protoParser struct {
	toks []string
	pos  int
	file *protoFile
	pkg  string
	// refs are the fields whose types are still to resolve, with their
	// scope.
	refs []protoRef
	// rpcs are the methods, with types still to resolve.
	rpcs []protoRPC
}

type protoRef struct {
	f     *protoField
	scope string
}

type protoRPC struct {
	name, in, out, scope string
	stream               bool
}

func (p *protoParser) next() string {
	if p.pos >= len(p.toks) {
		return ""
	}
	p.pos++
	return p.toks[p.pos-1]
}

func (p *protoParser) peek() string {
	if p.pos >= len(p.toks) {
		return ""
	}
	return p.toks[p.pos]
}

func (p *protoParser) expect(t string) error {
	if got := p.next(); got != t {
		return fmt.Errorf("expected %q, found %q", t, got)
	}
	return nil
}

// skipStatement skips to the end of a statement, or past a block.
func (p *protoParser) skipStatement() error {
	depth := 0
	for {
		switch p.next() {
		case "":
			return errors.New("the file ends too soon")
		case "{":
			depth++
		case "}":
			depth--
			if depth == 0 {
				return nil
			}
		case ";":
			if depth == 0 {
				return nil
			}
		}
	}
}

// parseProto reads a .proto file. Errors are meant for people.
func parseProto(src string) (*protoFile, error) {
	toks, err := protoTokens(src)
	if err != nil {
		return nil, fmt.Errorf("the protobuf definition is not valid: %w", err)
	}
	p := &protoParser{toks: toks, file: &protoFile{messages: map[string]*protoMessage{}, enums: map[string]*protoEnum{},
		methods: map[string]protoMethod{}}}
	p.file.messages["google.protobuf.Empty"] = &protoMessage{name: "google.protobuf.Empty"}
	if err := p.top(); err != nil {
		return nil, fmt.Errorf("the protobuf definition is not valid: %w", err)
	}
	if err := p.resolve(); err != nil {
		return nil, fmt.Errorf("the protobuf definition is not valid: %w", err)
	}
	return p.file, nil
}

func (p *protoParser) top() error {
	for p.peek() != "" {
		switch t := p.next(); t {
		case "package":
			p.pkg = p.next()
			if err := p.expect(";"); err != nil {
				return err
			}
		case "syntax", "edition", "import", "option":
			if err := p.skipStatement(); err != nil {
				return err
			}
		case "message":
			if err := p.message(p.pkg); err != nil {
				return err
			}
		case "enum":
			if err := p.enum(p.pkg); err != nil {
				return err
			}
		case "service":
			if err := p.service(); err != nil {
				return err
			}
		case ";":
		default:
			return fmt.Errorf("unexpected %q", t)
		}
	}
	return nil
}

func join(scope, name string) string {
	if scope == "" {
		return name
	}
	return scope + "." + name
}

func (p *protoParser) message(scope string) error {
	full := join(scope, p.next())
	m := &protoMessage{name: full}
	p.file.messages[full] = m
	if err := p.expect("{"); err != nil {
		return err
	}
	return p.body(m, full, false)
}

// body reads a message's (or a oneof's) body, up to its "}".
func (p *protoParser) body(m *protoMessage, scope string, oneof bool) error {
	for {
		switch t := p.peek(); t {
		case "":
			return errors.New("the file ends too soon")
		case "}":
			p.next()
			return nil
		case ";":
			p.next()
		case "message":
			p.next()
			if err := p.message(scope); err != nil {
				return err
			}
		case "enum":
			p.next()
			if err := p.enum(scope); err != nil {
				return err
			}
		case "oneof":
			p.next()
			p.next() // its name
			if err := p.expect("{"); err != nil {
				return err
			}
			if err := p.body(m, scope, true); err != nil {
				return err
			}
		case "option", "reserved", "extensions", "extend":
			if err := p.skipStatement(); err != nil {
				return err
			}
		default:
			if err := p.field(m, scope, oneof); err != nil {
				return err
			}
		}
	}
}

func (p *protoParser) field(m *protoMessage, scope string, oneof bool) error {
	f := &protoField{}
	t := p.next()
	switch t {
	case "repeated":
		f.repeated = true
		t = p.next()
	case "optional", "required":
		t = p.next()
	}
	if oneof && f.repeated {
		return errors.New("a oneof field cannot be repeated")
	}
	if t == "map" {
		if err := p.expect("<"); err != nil {
			return err
		}
		f.mapKey = p.next()
		if err := p.expect(","); err != nil {
			return err
		}
		t = p.next()
		if err := p.expect(">"); err != nil {
			return err
		}
	}
	f.typ = t
	f.name = p.next()
	if err := p.expect("="); err != nil {
		return err
	}
	n, err := strconv.Atoi(p.next())
	if err != nil || n < 1 || n > 536870911 {
		return fmt.Errorf("the field %s has no valid number", f.name)
	}
	f.number = n
	f.jsonName = lowerCamel(f.name)
	if p.peek() == "[" {
		for p.next() != "]" {
			if p.peek() == "" {
				return errors.New("the file ends too soon")
			}
		}
	}
	if err := p.expect(";"); err != nil {
		return err
	}
	m.fields = append(m.fields, f)
	if !scalars[f.typ] {
		p.refs = append(p.refs, protoRef{f, scope})
	}
	return nil
}

// lowerCamel is a field's JSON name.
func lowerCamel(s string) string {
	var b strings.Builder
	up := false
	for _, r := range s {
		if r == '_' {
			up = true
			continue
		}
		if up {
			r = unicode.ToUpper(r)
			up = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (p *protoParser) enum(scope string) error {
	full := join(scope, p.next())
	e := &protoEnum{values: map[string]int{}, names: map[int]string{}}
	p.file.enums[full] = e
	if err := p.expect("{"); err != nil {
		return err
	}
	for {
		t := p.next()
		switch t {
		case "":
			return errors.New("the file ends too soon")
		case "}":
			return nil
		case ";":
		case "option", "reserved":
			p.pos--
			if err := p.skipStatement(); err != nil {
				return err
			}
		default:
			if err := p.expect("="); err != nil {
				return err
			}
			n, err := strconv.Atoi(p.next())
			if err != nil {
				return fmt.Errorf("the enum value %s has no number", t)
			}
			e.values[t] = n
			if _, dup := e.names[n]; !dup {
				e.names[n] = t
			}
			if p.peek() == "[" {
				for p.next() != "]" {
					if p.peek() == "" {
						return errors.New("the file ends too soon")
					}
				}
			}
			if err := p.expect(";"); err != nil {
				return err
			}
		}
	}
}

func (p *protoParser) service() error {
	svc := join(p.pkg, p.next())
	if err := p.expect("{"); err != nil {
		return err
	}
	for {
		switch t := p.next(); t {
		case "":
			return errors.New("the file ends too soon")
		case "}":
			return nil
		case ";":
		case "option":
			p.pos--
			if err := p.skipStatement(); err != nil {
				return err
			}
		case "rpc":
			r := protoRPC{name: svc + "/" + p.next(), scope: p.pkg}
			var stream bool
			var err error
			if r.in, stream, err = p.rpcType(); err != nil {
				return err
			}
			r.stream = stream
			if err := p.expect("returns"); err != nil {
				return err
			}
			if r.out, stream, err = p.rpcType(); err != nil {
				return err
			}
			r.stream = r.stream || stream
			if p.peek() == "{" {
				p.pos--
				if err := p.skipStatement(); err != nil {
					return err
				}
			} else if err := p.expect(";"); err != nil {
				return err
			}
			p.rpcs = append(p.rpcs, r)
		default:
			return fmt.Errorf("unexpected %q in a service", t)
		}
	}
}

func (p *protoParser) rpcType() (string, bool, error) {
	if err := p.expect("("); err != nil {
		return "", false, err
	}
	t := p.next()
	stream := t == "stream"
	if stream {
		t = p.next()
	}
	return t, stream, p.expect(")")
}

// lookup finds a type's full name from a scope, inner scopes first.
func (p *protoParser) lookup(name, scope string) (string, bool) {
	if full, ok := strings.CutPrefix(name, "."); ok {
		_, m := p.file.messages[full]
		_, e := p.file.enums[full]
		return full, m || e
	}
	for {
		full := join(scope, name)
		if _, ok := p.file.messages[full]; ok {
			return full, true
		}
		if _, ok := p.file.enums[full]; ok {
			return full, true
		}
		if scope == "" {
			return "", false
		}
		i := strings.LastIndex(scope, ".")
		if i < 0 {
			scope = ""
		} else {
			scope = scope[:i]
		}
	}
}

func (p *protoParser) resolve() error {
	for _, r := range p.refs {
		full, ok := p.lookup(r.f.typ, r.scope)
		if !ok {
			return fmt.Errorf("unknown type %s (imports are not read)", r.f.typ)
		}
		r.f.typ = full
	}
	for _, r := range p.rpcs {
		in, ok := p.lookup(r.in, r.scope)
		if !ok || p.file.messages[in] == nil {
			return fmt.Errorf("unknown message %s", r.in)
		}
		out, ok := p.lookup(r.out, r.scope)
		if !ok || p.file.messages[out] == nil {
			return fmt.Errorf("unknown message %s", r.out)
		}
		p.file.methods[r.name] = protoMethod{in: in, out: out, stream: r.stream}
	}
	return nil
}

// Encoding: JSON to the wire format.

func (f *protoField) wireType() int {
	switch f.typ {
	case "double", "fixed64", "sfixed64":
		return 1
	case "float", "fixed32", "sfixed32":
		return 5
	case "string", "bytes":
		return 2
	}
	if scalars[f.typ] {
		return 0
	}
	return -1 // a message (2) or an enum (0): the file says
}

func tag(b []byte, number, wire int) []byte {
	return binary.AppendUvarint(b, uint64(number)<<3|uint64(wire)) //nolint:gosec // G115: numbers are positive
}

// encodeMessage encodes a JSON object as the message name.
func (pf *protoFile) encodeMessage(name string, raw json.RawMessage) ([]byte, error) {
	m := pf.messages[name]
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("%s must be a JSON object", m.name)
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []byte
	for _, k := range keys {
		f := m.field(k)
		if f == nil {
			return nil, fmt.Errorf("%s has no field %s", m.name, k)
		}
		v := obj[k]
		if string(v) == "null" {
			continue
		}
		var err error
		switch {
		case f.mapKey != "":
			out, err = pf.encodeMap(out, f, v)
		case f.repeated:
			var items []json.RawMessage
			if err := json.Unmarshal(v, &items); err != nil {
				return nil, fmt.Errorf("%s must be a list", k)
			}
			for _, it := range items {
				if out, err = pf.encodeValue(out, f.number, f.typ, it); err != nil {
					break
				}
			}
		default:
			out, err = pf.encodeValue(out, f.number, f.typ, v)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
	}
	return out, nil
}

func (m *protoMessage) field(name string) *protoField {
	for _, f := range m.fields {
		if f.name == name || f.jsonName == name {
			return f
		}
	}
	return nil
}

func (pf *protoFile) encodeMap(out []byte, f *protoField, v json.RawMessage) ([]byte, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(v, &obj); err != nil {
		return nil, errors.New("must be a JSON object")
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		key := json.RawMessage(strconv.Quote(k))
		if f.mapKey != "string" {
			key = json.RawMessage(k)
		}
		entry, err := pf.encodeValue(nil, 1, f.mapKey, key)
		if err != nil {
			return nil, err
		}
		if entry, err = pf.encodeValue(entry, 2, f.typ, obj[k]); err != nil {
			return nil, err
		}
		out = tag(out, f.number, 2)
		out = binary.AppendUvarint(out, uint64(len(entry)))
		out = append(out, entry...)
	}
	return out, nil
}

// jsonNumber reads a number, or a number in a string as proto3 JSON writes
// 64-bit integers.
func jsonNumber(v json.RawMessage) (string, error) {
	var s string
	if json.Unmarshal(v, &s) == nil {
		return s, nil
	}
	var n json.Number
	if err := json.Unmarshal(v, &n); err != nil {
		return "", errors.New("must be a number")
	}
	return n.String(), nil
}

func (pf *protoFile) encodeValue(out []byte, number int, typ string, v json.RawMessage) ([]byte, error) {
	if pf.messages[typ] != nil {
		b, err := pf.encodeMessage(typ, v)
		if err != nil {
			return nil, err
		}
		out = tag(out, number, 2)
		out = binary.AppendUvarint(out, uint64(len(b)))
		return append(out, b...), nil
	}
	if e := pf.enums[typ]; e != nil {
		var name string
		if json.Unmarshal(v, &name) == nil {
			n, ok := e.values[name]
			if !ok {
				return nil, fmt.Errorf("%s is not a value of %s", name, typ)
			}
			return binary.AppendUvarint(tag(out, number, 0), uint64(int64(n))), nil //nolint:gosec // G115: negative enums are 10-byte varints
		}
		typ = "int32"
	}
	switch typ {
	case "string":
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return nil, errors.New("must be a string")
		}
		out = tag(out, number, 2)
		out = binary.AppendUvarint(out, uint64(len(s)))
		return append(out, s...), nil
	case "bytes":
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return nil, errors.New("must be a base64 string")
		}
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return nil, errors.New("must be a base64 string")
		}
		out = tag(out, number, 2)
		out = binary.AppendUvarint(out, uint64(len(b)))
		return append(out, b...), nil
	case "bool":
		var b bool
		if err := json.Unmarshal(v, &b); err != nil {
			return nil, errors.New("must be true or false")
		}
		n := uint64(0)
		if b {
			n = 1
		}
		return binary.AppendUvarint(tag(out, number, 0), n), nil
	}
	s, err := jsonNumber(v)
	if err != nil {
		return nil, err
	}
	return encodeNumber(out, number, typ, s)
}

func encodeNumber(out []byte, number int, typ, s string) ([]byte, error) {
	bits := 64
	if strings.HasSuffix(typ, "32") || typ == "float" {
		bits = 32
	}
	switch typ {
	case "double", "float":
		f, err := strconv.ParseFloat(s, bits)
		if err != nil {
			return nil, errors.New("must be a number")
		}
		if typ == "float" {
			return binary.LittleEndian.AppendUint32(tag(out, number, 5), math.Float32bits(float32(f))), nil
		}
		return binary.LittleEndian.AppendUint64(tag(out, number, 1), math.Float64bits(f)), nil
	case "uint32", "uint64", "fixed32", "fixed64":
		u, err := strconv.ParseUint(s, 10, bits)
		if err != nil {
			return nil, fmt.Errorf("must be a whole number from 0, of %d bits", bits)
		}
		switch typ {
		case "fixed32":
			return binary.LittleEndian.AppendUint32(tag(out, number, 5), uint32(u)), nil //nolint:gosec // G115: parsed as 32 bits
		case "fixed64":
			return binary.LittleEndian.AppendUint64(tag(out, number, 1), u), nil
		}
		return binary.AppendUvarint(tag(out, number, 0), u), nil
	}
	i, err := strconv.ParseInt(s, 10, bits)
	if err != nil {
		return nil, fmt.Errorf("must be a whole number of %d bits", bits)
	}
	switch typ {
	case "sint32", "sint64":
		return binary.AppendUvarint(tag(out, number, 0), uint64(i<<1)^uint64(i>>63)), nil //nolint:gosec // G115: zigzag
	case "sfixed32":
		return binary.LittleEndian.AppendUint32(tag(out, number, 5), uint32(i)), nil //nolint:gosec // G115: two's complement
	case "sfixed64":
		return binary.LittleEndian.AppendUint64(tag(out, number, 1), uint64(i)), nil //nolint:gosec // G115: two's complement
	}
	return binary.AppendUvarint(tag(out, number, 0), uint64(i)), nil //nolint:gosec // G115: negative ints are 10-byte varints
}

// Decoding: the wire format to JSON values.

var errWire = errors.New("the answer is not valid protobuf")

// decodeMessage decodes a message into a JSON-ready map.
func (pf *protoFile) decodeMessage(m *protoMessage, b []byte, depth int) (map[string]any, error) {
	if depth > 64 {
		return nil, errWire
	}
	out := map[string]any{}
	for len(b) > 0 {
		key, n := binary.Uvarint(b)
		if n <= 0 {
			return nil, errWire
		}
		b = b[n:]
		number, wire := int(key>>3), int(key&7) //nolint:gosec // G115: field numbers fit
		var raw []byte
		var num uint64
		switch wire {
		case 0:
			num, n = binary.Uvarint(b)
			if n <= 0 {
				return nil, errWire
			}
		case 1:
			if len(b) < 8 {
				return nil, errWire
			}
			num, n = binary.LittleEndian.Uint64(b), 8
		case 5:
			if len(b) < 4 {
				return nil, errWire
			}
			num, n = uint64(binary.LittleEndian.Uint32(b)), 4
		case 2:
			l, k := binary.Uvarint(b)
			if k <= 0 || l > uint64(len(b)-k) { //nolint:gosec // G115: k is at most len(b)
				return nil, errWire
			}
			raw, n = b[k:k+int(l)], k+int(l) //nolint:gosec // G115: checked against the length
		default:
			return nil, errWire
		}
		b = b[n:]
		var f *protoField
		for _, x := range m.fields {
			if x.number == number {
				f = x
			}
		}
		if f == nil {
			continue
		}
		if err := pf.decodeField(out, f, wire, num, raw, depth); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (pf *protoFile) decodeField(out map[string]any, f *protoField, wire int, num uint64, raw []byte, depth int) error {
	if f.mapKey != "" {
		entry := &protoMessage{fields: []*protoField{{name: "key", jsonName: "key", number: 1, typ: f.mapKey}, {name: "value", jsonName: "value", number: 2, typ: f.typ}}}
		e, err := pf.decodeMessage(entry, raw, depth+1)
		if err != nil {
			return err
		}
		mp, _ := out[f.jsonName].(map[string]any)
		if mp == nil {
			mp = map[string]any{}
			out[f.jsonName] = mp
		}
		mp[fmt.Sprint(e["key"])] = e["value"]
		return nil
	}
	if f.repeated && wire == 2 && f.typ != "string" && f.typ != "bytes" && pf.messages[f.typ] == nil {
		return pf.decodePacked(out, f, raw)
	}
	var v any
	switch {
	case pf.messages[f.typ] != nil:
		if wire != 2 {
			return errWire
		}
		sub, err := pf.decodeMessage(pf.messages[f.typ], raw, depth+1)
		if err != nil {
			return err
		}
		v = sub
	case f.typ == "string":
		v = string(raw)
	case f.typ == "bytes":
		v = base64.StdEncoding.EncodeToString(raw)
	default:
		v = pf.scalar(f.typ, num)
	}
	if f.repeated {
		list, _ := out[f.jsonName].([]any)
		out[f.jsonName] = append(list, v)
	} else {
		out[f.jsonName] = v
	}
	return nil
}

// decodePacked decodes packed repeated scalars.
func (pf *protoFile) decodePacked(out map[string]any, f *protoField, raw []byte) error {
	for len(raw) > 0 {
		var v uint64
		var n int
		switch f.wireType() {
		case 1:
			if len(raw) < 8 {
				return errWire
			}
			v, n = binary.LittleEndian.Uint64(raw), 8
		case 5:
			if len(raw) < 4 {
				return errWire
			}
			v, n = uint64(binary.LittleEndian.Uint32(raw)), 4
		default:
			if v, n = binary.Uvarint(raw); n <= 0 {
				return errWire
			}
		}
		raw = raw[n:]
		list, _ := out[f.jsonName].([]any)
		out[f.jsonName] = append(list, pf.scalar(f.typ, v))
	}
	return nil
}

// scalar turns a number from the wire into its JSON value.
func (pf *protoFile) scalar(typ string, v uint64) any {
	if e := pf.enums[typ]; e != nil {
		if name, ok := e.names[int(int32(v))]; ok { //nolint:gosec // G115: enums are int32
			return name
		}
		return int32(v) //nolint:gosec // G115: enums are int32
	}
	switch typ {
	case "bool":
		return v != 0
	case "double":
		return math.Float64frombits(v)
	case "float":
		return math.Float32frombits(uint32(v)) //nolint:gosec // G115: 32 bits on the wire
	case "int32", "sfixed32":
		return int32(v) //nolint:gosec // G115: two's complement
	case "int64", "sfixed64":
		return strconv.FormatInt(int64(v), 10) //nolint:gosec // G115: two's complement
	case "uint32", "fixed32":
		return uint32(v) //nolint:gosec // G115: 32 bits on the wire
	case "sint32":
		return int32(v>>1) ^ -int32(v&1) //nolint:gosec // G115: zigzag
	case "sint64":
		return strconv.FormatInt(int64(v>>1)^-int64(v&1), 10) //nolint:gosec // G115: zigzag
	}
	return strconv.FormatUint(v, 10)
}
