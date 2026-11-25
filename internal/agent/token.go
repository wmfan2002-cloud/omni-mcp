package agent

import (
	"fmt"
	"os"
	"strings"
)

const agentTokenPrefix = "OMNI_AGENT_TOKEN_"

func TokenEnvName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !validAgentName.MatchString(name) || name == "local" {
		return "", fmt.Errorf("agent name must use lowercase letters, numbers, and hyphens, and cannot be local")
	}
	return agentTokenPrefix + strings.ToUpper(strings.ReplaceAll(name, "-", "_")), nil
}

func TokensFromEnvironment(environ []string) map[string]string {
	tokens := map[string]string{}
	for _, item := range environ {
		key, value, found := strings.Cut(item, "=")
		if !found || !strings.HasPrefix(key, agentTokenPrefix) {
			continue
		}
		name := strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(key, agentTokenPrefix), "_", "-"))
		if validAgentName.MatchString(name) && name != "local" && strings.TrimSpace(value) != "" {
			tokens[name] = strings.TrimSpace(value)
		}
	}
	return tokens
}

func tokensFromProcessEnvironment() map[string]string {
	return TokensFromEnvironment(os.Environ())
}
