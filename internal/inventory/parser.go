package inventory

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// HostInfo is one resolved host record from inventory.
type HostInfo struct {
	Alias          string
	Host           string
	User           string
	Port           int
	PrivateKeyFile string
	Password       string
	Groups         []string
}

// Inventory stores dual indexes for fast alias/group lookup.
type Inventory struct {
	AliasToHost  map[string]HostInfo
	GroupToAlias map[string][]string

	DefaultUser string
	DefaultPort int
	DefaultKey  string
	DefaultPass string
}

// ParseINI parses an Ansible-INI style hosts file and builds dual indexes.
func ParseINI(content string) (*Inventory, error) {
	inv := &Inventory{
		AliasToHost:  make(map[string]HostInfo),
		GroupToAlias: make(map[string][]string),
		DefaultPort:  22,
	}

	currentGroup := ""
	sc := bufio.NewScanner(strings.NewReader(content))
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentGroup = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		if currentGroup == "" {
			return nil, fmt.Errorf("line %d: host entry outside of section", lineNo)
		}
		if strings.EqualFold(currentGroup, "all:vars") {
			if err := parseGlobalVars(inv, line, lineNo); err != nil {
				return nil, err
			}
			continue
		}
		if strings.Contains(currentGroup, ":") {
			// Ignore advanced meta sections (children, vars on specific group) for now.
			continue
		}
		h, err := parseHostLine(line, lineNo)
		if err != nil {
			return nil, err
		}
		h.Groups = appendUnique(h.Groups, currentGroup)
		inv.AliasToHost[h.Alias] = h
		inv.GroupToAlias[currentGroup] = appendUnique(inv.GroupToAlias[currentGroup], h.Alias)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	// Apply defaults after full parse so [all:vars] works regardless of section order.
	for alias, h := range inv.AliasToHost {
		applyDefaults(&h, inv)
		inv.AliasToHost[alias] = h
	}
	return inv, nil
}

func parseGlobalVars(inv *Inventory, line string, lineNo int) error {
	k, v, ok := strings.Cut(line, "=")
	if !ok {
		return fmt.Errorf("line %d: invalid var, expect key=value", lineNo)
	}
	key := strings.TrimSpace(strings.ToLower(k))
	val := strings.TrimSpace(v)
	switch key {
	case "ansible_user":
		inv.DefaultUser = val
	case "ansible_port":
		p, err := strconv.Atoi(val)
		if err != nil || p <= 0 || p > 65535 {
			return fmt.Errorf("line %d: invalid ansible_port %q", lineNo, val)
		}
		inv.DefaultPort = p
	case "ansible_ssh_private_key_file":
		inv.DefaultKey = expandHome(val)
	case "ansible_password", "ansible_ssh_pass":
		inv.DefaultPass = val
	}
	return nil
}

func parseHostLine(line string, lineNo int) (HostInfo, error) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return HostInfo{}, fmt.Errorf("line %d: empty host line", lineNo)
	}
	h := HostInfo{
		Alias: fields[0],
		Port:  0,
	}
	for _, f := range fields[1:] {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			continue
		}
		key := strings.TrimSpace(strings.ToLower(k))
		val := strings.TrimSpace(v)
		switch key {
		case "ansible_host":
			h.Host = val
		case "ansible_user":
			h.User = val
		case "ansible_port":
			p, err := strconv.Atoi(val)
			if err != nil || p <= 0 || p > 65535 {
				return HostInfo{}, fmt.Errorf("line %d: invalid ansible_port %q", lineNo, val)
			}
			h.Port = p
		case "ansible_ssh_private_key_file":
			h.PrivateKeyFile = expandHome(val)
		case "ansible_password", "ansible_ssh_pass":
			h.Password = val
		}
	}
	if h.Host == "" {
		// ansible inventory usually allows omitted host and uses alias as host.
		h.Host = h.Alias
	}
	return h, nil
}

func applyDefaults(h *HostInfo, inv *Inventory) {
	if h.User == "" {
		h.User = inv.DefaultUser
	}
	if h.Port == 0 {
		h.Port = inv.DefaultPort
	}
	if h.PrivateKeyFile == "" {
		h.PrivateKeyFile = inv.DefaultKey
	}
	if h.Password == "" {
		h.Password = inv.DefaultPass
	}
}

func appendUnique(xs []string, v string) []string {
	for _, x := range xs {
		if x == v {
			return xs
		}
	}
	return append(xs, v)
}

func expandHome(path string) string {
	if path == "" || !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~/"))
}

// LoadINIFromFile parses inventory from file path.
func LoadINIFromFile(path string) (*Inventory, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseINI(string(b))
}

// ResolveAlias returns one fully-resolved connection object by alias.
func (i *Inventory) ResolveAlias(alias string) (HostInfo, bool) {
	if i == nil {
		return HostInfo{}, false
	}
	h, ok := i.AliasToHost[alias]
	return h, ok
}

// ResolveGroup returns fully-resolved connection objects for a group.
func (i *Inventory) ResolveGroup(group string) []HostInfo {
	if i == nil {
		return nil
	}
	aliases := i.GroupToAlias[group]
	out := make([]HostInfo, 0, len(aliases))
	for _, a := range aliases {
		if h, ok := i.AliasToHost[a]; ok {
			out = append(out, h)
		}
	}
	return out
}

func (i *Inventory) Aliases() []string {
	if i == nil {
		return nil
	}
	out := make([]string, 0, len(i.AliasToHost))
	for a := range i.AliasToHost {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

func (i *Inventory) Groups() []string {
	if i == nil {
		return nil
	}
	out := make([]string, 0, len(i.GroupToAlias))
	for g := range i.GroupToAlias {
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}
