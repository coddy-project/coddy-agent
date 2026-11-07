//go:build memory

package memtools

// Tool names for the memory subagent (never exposed to the main agent).
const (
	NameSearch = "coddy_memory_search"
	NameList   = "coddy_memory_list"
	NameRead   = "coddy_memory_read"
	NameMkdir  = "coddy_memory_mkdir"
	NameSave   = "coddy_memory_save"
	NameDelete = "coddy_memory_delete"
)
