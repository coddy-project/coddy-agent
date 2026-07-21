package config

func boolPtr(v bool) *bool { return &v }

func intPtr(v int) *int { return &v }

// SchemaExampleConfigJSON returns representative defaults for JSON Schema "default"
// and UI placeholders. It is not loaded as a real config; values mirror applyDefaults
// and field semantics where possible.
func SchemaExampleConfigJSON() *ConfigJSON {
	return &ConfigJSON{
		Providers: []ProviderJSON{
			{Name: "openai", Type: "openai", APIBase: "", APIKey: ""},
		},
		Models: []ModelJSON{
			{
				Model:            "openai/gpt-4o",
				MaxTokens:        4096,
				Temperature:      0.2,
				MaxContextTokens: 0,
			},
		},
		Agent: AgentJSON{
			Model:            "openai/gpt-4o",
			MaxTurns:         AgentDefaultMaxTurns,
			MaxTokensPerTurn: AgentDefaultMaxTokensPerTurn,
			LLMRetryMax:      AgentDefaultLLMRetryMax,
			LLMRetryBaseMS:   AgentDefaultLLMRetryBaseMS,
		},
		Prompts: PromptsJSON{
			Dir:         "",
			AgentPrompt: "agent.md",
			PlanPrompt:  "plan.md",
		},
		Instructions: InstructionsJSON{
			Files: []string{"AGENTS.md"},
		},
		Skills: SkillsJSON{
			Dirs: []string{
				"~/.agents/skills",
				"${CODDY_HOME}/skills",
				"${CWD}/.coddy/skills",
			},
		},
		MCPServers: []MCPServerJSON{},
		Tools: ToolsJSON{
			PermissionMode:   PermModeAsk,
			CommandAllowlist: nil,
		},
		Logger: LoggerJSON{
			Level:    LogLevelInfo,
			Outputs:  []string{LogOutputStderr},
			File:     "",
			Format:   "text",
			Rotation: LoggerRotationJSON{MaxSizeMB: 0, MaxFiles: 0},
		},
		Sessions: SessionsJSON{Dir: ""},
		Compaction: CompactionJSON{
			Enabled:          boolPtr(true),
			ThresholdPercent: CompactionDefaultThresholdPercent,
			KeepRecentTurns:  intPtr(CompactionDefaultKeepRecentTurns),
			Model:            "",
		},
		Memory: MemoryJSON{
			Enabled:          false,
			Model:            "",
			Dir:              "",
			RecallMaxTurns:   6,
			PersistMaxTurns:  12,
			CopilotMaxTokens: 4096,
			MaxSearchHits:    8,
		},
		Scheduler: SchedulerJSON{
			Enabled:        false,
			Dir:            "${CODDY_HOME}/scheduler",
			MaxQueue:       10,
			Timeout:        "30m",
			RetainSessions: 5,
		},
		Gateways: GatewaysJSON{
			Telegram: TelegramGatewayJSON{
				Enabled:          false,
				Token:            "${TELEGRAM_BOT_TOKEN}",
				RichMessages:     true,
				DefaultAccess:    string(AccessAll),
				DefaultIsolation: string(IsolationIndividual),
			},
		},
	}
}
