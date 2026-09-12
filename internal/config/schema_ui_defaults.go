package config

func boolPtr(v bool) *bool { return &v }

func intPtr(v int) *int { return &v }

// SchemaExampleConfigJSON returns representative defaults for JSON Schema "default"
// and UI placeholders. It is not loaded as a real config; values mirror applyDefaults
// and field semantics where possible.
//
// Each value is the placeholder of its own field, not part of a coherent row:
// attachNodeDefaults walks the tree and attaches a "default" per property. So
// the temperature here is what a new models[] row offers for temperature
// whatever model it names - including a reasoning id such as the one below,
// which would not send it (see the reasoning branch in internal/llm/openai.go).
func SchemaExampleConfigJSON() *ConfigJSON {
	return &ConfigJSON{
		Providers: []ProviderJSON{
			{Name: "openai", Type: "openai", APIBase: "", APIKey: ""},
		},
		Models: []ModelJSON{
			{
				Model:            "openai/gpt-5.6-terra",
				MaxTokens:        4096,
				Temperature:      0.2,
				MaxContextTokens: 0,
			},
		},
		Agent: AgentJSON{
			Model:                  "openai/gpt-5.6-terra",
			MaxTurns:               AgentDefaultMaxTurns,
			MaxTokensPerTurn:       AgentDefaultMaxTokensPerTurn,
			LLMRetryMax:            intPtr(AgentDefaultLLMRetryMax),
			LLMRetryBaseMS:         AgentDefaultLLMRetryBaseMS,
			LLMFirstTokenTimeoutMS: intPtr(AgentDefaultLLMFirstTokenTimeoutMS),
			LoopGuard:              boolPtr(true),
			LoopToolRepeatLimit:    intPtr(AgentDefaultLoopToolRepeatLimit),
			LoopStreamRepeatCycles: intPtr(AgentDefaultLoopStreamRepeatCycles),
			LoopNudgeMax:           intPtr(AgentDefaultLoopNudgeMax),
			WaitForLimitResetMaxMS: intPtr(AgentDefaultWaitForLimitResetMaxMS),
		},
		Prompts: PromptsJSON{
			Dir:         "",
			AgentPrompt: "agent.md",
			PlanPrompt:  "plan.md",
			AskPrompt:   "ask.md",
		},
		Instructions: InstructionsJSON{
			Files: DefaultInstructionFiles(),
		},
		Skills: SkillsJSON{
			Dirs: []string{
				"~/.agents/skills",
				"${CODDY_HOME}/skills",
				"${CWD}/.coddy/skills",
			},
		},
		MCPServers: []MCPServerJSON{},
		MCP:        MCPJSON{ProjectTrust: ProjectTrustAsk},
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
		Subagents: SubagentsJSON{
			Enabled:               boolPtr(true),
			Dirs:                  DefaultSubagentDirs(),
			ProjectTrust:          SubagentsProjectTrustAsk,
			MaxConcurrent:         SubagentsDefaultMaxConcurrent,
			MaxDepth:              intPtr(SubagentsDefaultMaxDepth),
			DefaultTimeoutSeconds: SubagentsDefaultTimeoutSeconds,
			MaxTurns:              0,
		},
		Hooks: HooksJSON{
			Enabled:               boolPtr(true),
			Files:                 DefaultHookFiles(),
			ProjectTrust:          ProjectTrustAsk,
			DefaultTimeoutSeconds: HooksDefaultTimeoutSeconds,
			StopLoopLimit:         HooksDefaultStopLoopLimit,
			MaxOutputChars:        HooksDefaultMaxOutputChars,
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
