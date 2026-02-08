package client

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

const (
	// DefaultConfigPath is the default path to the config file
	DefaultConfigPath = "/etc/config/cloud-config"

	// ConfigSectionVCloud is the vCloud section name in config
	ConfigSectionVCloud = "vCloud"

	// ConfigKeyMgmtURL is the key for management URL
	ConfigKeyMgmtURL = "MGMT_URL"

	// ConfigKeyProviderToken is the key for provider token
	ConfigKeyProviderToken = "PROVIDER_TOKEN"
)

// Config holds vCloud API configuration
type Config struct {
	APIURL        string
	ProviderToken string
}

// LoadConfig loads config from the default path
func LoadConfig() (*Config, error) {
	return LoadConfigFromFile(DefaultConfigPath)
}

// LoadConfigFromFile loads config from an INI file
func LoadConfigFromFile(filePath string) (*Config, error) {
	sections, err := parseINIFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	vcloudSection, ok := sections[ConfigSectionVCloud]
	if !ok {
		return nil, fmt.Errorf("missing [%s] section in config file", ConfigSectionVCloud)
	}

	apiURL, ok := vcloudSection[ConfigKeyMgmtURL]
	if !ok || apiURL == "" {
		return nil, fmt.Errorf("missing %s in [%s] section", ConfigKeyMgmtURL, ConfigSectionVCloud)
	}

	providerToken, ok := vcloudSection[ConfigKeyProviderToken]
	if !ok || providerToken == "" {
		return nil, fmt.Errorf("missing %s in [%s] section", ConfigKeyProviderToken, ConfigSectionVCloud)
	}

	return &Config{
		APIURL:        apiURL,
		ProviderToken: providerToken,
	}, nil
}

// parseINIFile parses an INI file and returns sections with key-value pairs
func parseINIFile(filePath string) (map[string]map[string]string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	sections := make(map[string]map[string]string)
	var currentSection string

	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		// Check for section header
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentSection = strings.TrimPrefix(strings.TrimSuffix(line, "]"), "[")
			currentSection = strings.TrimSpace(currentSection)
			if _, exists := sections[currentSection]; !exists {
				sections[currentSection] = make(map[string]string)
			}
			continue
		}

		// Parse key=value pair
		if currentSection == "" {
			continue // Ignore key-value pairs outside sections
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue // Skip malformed lines
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		// Remove surrounding quotes if present
		value = strings.Trim(value, `"'`)

		sections[currentSection][key] = value
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading config file at line %d: %w", lineNum, err)
	}

	return sections, nil
}

// Validate validates the config
func (c *Config) Validate() error {
	if c.APIURL == "" {
		return fmt.Errorf("API URL is required")
	}
	if c.ProviderToken == "" {
		return fmt.Errorf("provider token is required")
	}
	return nil
}

// NewClientFromConfig creates a new Client from config
func NewClientFromConfig(cfg *Config) *Client {
	return NewClient(cfg.APIURL, cfg.ProviderToken)
}
