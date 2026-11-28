package manifest

// MaxMCPServerNameLen is the longest declared name of an mcp-server artifact.
const MaxMCPServerNameLen = 64

// ValidMCPServerName reports whether name meets the §4.1 MCP server name
// rule: 1 to 64 characters, each an ASCII letter, an ASCII digit, '_', or
// '-'. The name of an mcp-server is the key of its entry in harness
// configuration files, so a name outside this set could address another
// table of a TOML file, or add one.
//
// Spec: §4.1 (MCP server names)
func ValidMCPServerName(name string) bool {
	if name == "" || len(name) > MaxMCPServerNameLen {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

// MCPServerNameRefused reports whether a is an mcp-server artifact whose
// declared name breaks the §4.1 MCP server name rule. An artifact that
// declares no name takes the last segment of its ID as its key, which the
// adapters encode, so it is not refused.
//
// Spec: §4.1 (MCP server names)
func MCPServerNameRefused(a *Artifact) bool {
	return a != nil && a.Type == TypeMCPServer && a.Name != "" && !ValidMCPServerName(a.Name)
}
