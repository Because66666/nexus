package agent

import _ "embed"

//go:embed prompt_base.md
var defaultBaseSystemPrompt string

//go:embed prompt_main_agent.md
var defaultMainAgentSystemPrompt string

//go:embed prompt_platform_darwin.md
var defaultPlatformDarwinPrompt string

//go:embed prompt_platform_windows.md
var defaultPlatformWindowsPrompt string
