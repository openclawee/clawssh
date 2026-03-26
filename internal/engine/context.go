package engine

// EnvContext is serialized into the LLM user payload to ground parsing in the current machine.
type EnvContext struct {
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Hostname string `json:"hostname,omitempty"`
	// Infra contains lightweight probe outputs collected at session start.
	Infra map[string]string `json:"infra,omitempty"`
	// InventoryAliases are host aliases available to this gateway.
	InventoryAliases []string `json:"inventory_aliases,omitempty"`
	// InventoryGroups are group names available to this gateway.
	InventoryGroups []string `json:"inventory_groups,omitempty"`
}

// Context carries session-scoped metadata from the SSH layer into intent parsing.
type Context struct {
	Username   string
	RemoteAddr string
	SessionID  string
	// Env describes the gateway host environment when executing locally.
	Env *EnvContext
}
