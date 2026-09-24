package probe

import (
	"encoding/json"
	"regexp"
)

// identityKeyRE names keys whose values identify one unit (MAC, serial,
// GPON/PLOAM, credentials, radio identity). Their values never enter the
// profile or the reports; the key stays with "<redacted>" so its existence
// is still visible.
var identityKeyRE = regexp.MustCompile(`(?i)^(mac|macaddr|mac_?addr(ess)?|[a-z0-9_]*[_-]mac(addr(ess)?)?|local-mac-address|hwaddr|eth[0-9]*addr|serial[#a-z_]*|sn|[a-z0-9_]*_sn|gpon[a-z0-9_]*|ploam[a-z0-9_]*|[a-z0-9_]*passw(or)?d[a-z0-9_]*|[a-z0-9_]*secret[a-z0-9_]*|[a-z0-9_]*token[a-z0-9_]*|[a-z0-9_]*_key|psk|wpa_?key|wps[a-z0-9_]*|loid[a-z0-9_]*|imei|imsi|iccid|board_?serial)$`)

const redacted = "<redacted>"

// sanitize returns a copy of a JSON-like value with identity values removed:
// keys matching identityKeyRE get "<redacted>", MAC addresses in any string
// are masked. It returns how many values were changed.
func sanitize(v any) (any, int) {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		n := 0
		for k, val := range x {
			if identityKeyRE.MatchString(k) {
				if val != nil && val != redacted {
					n++
				}
				out[k] = redacted
				continue
			}
			sv, c := sanitize(val)
			out[k] = sv
			n += c
		}
		return out, n
	case []any:
		out := make([]any, len(x))
		n := 0
		for i, val := range x {
			sv, c := sanitize(val)
			out[i] = sv
			n += c
		}
		return out, n
	case string:
		if y := macRE.ReplaceAllString(x, "xx:xx:xx:xx:xx:xx"); y != x {
			return y, 1
		}
		return x, 0
	}
	return v, 0
}

// sanitizedJSON marshals v and runs sanitize over the generic form.
func sanitizedJSON(v any) ([]byte, int, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, 0, err
	}
	var g any
	if err := json.Unmarshal(b, &g); err != nil {
		return nil, 0, err
	}
	s, n := sanitize(g)
	out, err := json.MarshalIndent(s, "", "  ")
	return out, n, err
}
