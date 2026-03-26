package dsl

// Core action identifiers for the LLM intent layer and policy allowlists.
// Adapters may only implement a subset; unimplemented actions should fail gracefully at execution time.
const (
	ActionCheckCPU       = "check_cpu"
	ActionCheckDisk      = "check_disk"
	ActionCheckMemory    = "check_memory"
	ActionCheckNetwork   = "check_network"
	ActionListProcesses  = "list_processes"
	ActionServiceStatus  = "service_status"
	ActionServiceControl = "service_control"
	ActionTailLog        = "tail_log"
	ActionReadFile       = "read_file"
	ActionListDirectory  = "list_directory"
	ActionPingHost       = "ping_host"
	ActionPortCheck      = "port_check"
	ActionDNSLookup      = "dns_lookup"
	ActionUptimeInfo     = "uptime_info"
	ActionWhoamiContext  = "whoami_context"
	ActionEnvSnapshot    = "env_snapshot"
	ActionPackageList    = "package_list"
	ActionDiskInodeCheck = "disk_inode_check"
	ActionFirewallStatus = "firewall_status"
	ActionExplainIntent  = "explain_intent"
)

// CoreActionIDs returns the canonical list used in JSON Schema and prompts (stable order).
func CoreActionIDs() []string {
	return []string{
		ActionCheckCPU,
		ActionCheckDisk,
		ActionCheckMemory,
		ActionCheckNetwork,
		ActionListProcesses,
		ActionServiceStatus,
		ActionServiceControl,
		ActionTailLog,
		ActionReadFile,
		ActionListDirectory,
		ActionPingHost,
		ActionPortCheck,
		ActionDNSLookup,
		ActionUptimeInfo,
		ActionWhoamiContext,
		ActionEnvSnapshot,
		ActionPackageList,
		ActionDiskInodeCheck,
		ActionFirewallStatus,
		ActionExplainIntent,
	}
}
