package migrate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/sipeed/picoclaw/pkg/config"
)

var supportedProviders = map[string]bool{
	"anthropic":  true,
	"openai":     true,
	"openrouter": true,
	"groq":       true,
	"zhipu":      true,
	"vllm":       true,
	"gemini":     true,
}

var supportedChannels = map[string]bool{
	"telegram":    true,
	"discord":     true,
	"whatsapp":    true,
	"feishu":      true,
	"qq":          true,
	"dingtalk":    true,
	"maixcam":     true,
	"bluebubbles": true,
}

func findOpenClawConfig(openclawHome string) (string, error) {
	candidates := []string{
		filepath.Join(openclawHome, "openclaw.json"),
		filepath.Join(openclawHome, "config.json"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("no config file found in %s (tried openclaw.json, config.json)", openclawHome)
}

func LoadOpenClawConfig(configPath string) (map[string]interface{}, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("reading OpenClaw config: %w", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing OpenClaw config: %w", err)
	}

	converted := convertKeysToSnake(raw)
	result, ok := converted.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected config format")
	}
	return result, nil
}

func ConvertConfig(data map[string]interface{}) (*config.Config, []string, error) {
	cfg := config.DefaultConfig()
	var warnings []string

	if agents, ok := getMap(data, "agents"); ok {
		if defaults, ok := getMap(agents, "defaults"); ok {
			if v, ok := getString(defaults, "model"); ok {
				cfg.Agents.Defaults.Model = v
			}
			if v, ok := getFloat(defaults, "max_tokens"); ok {
				cfg.Agents.Defaults.MaxTokens = int(v)
			}
			if v, ok := getFloat(defaults, "temperature"); ok {
				cfg.Agents.Defaults.Temperature = v
			}
			if v, ok := getFloat(defaults, "max_tool_iterations"); ok {
				cfg.Agents.Defaults.MaxToolIterations = int(v)
			}
			if v, ok := getString(defaults, "workspace"); ok {
				cfg.Agents.Defaults.Workspace = rewriteWorkspacePath(v)
			}
		}
	}

	if providers, ok := getMap(data, "providers"); ok {
		for name, val := range providers {
			pMap, ok := val.(map[string]interface{})
			if !ok {
				continue
			}
			apiKey, _ := getString(pMap, "api_key")
			apiBase, _ := getString(pMap, "api_base")

			if !supportedProviders[name] {
				if apiKey != "" || apiBase != "" {
					warnings = append(warnings, fmt.Sprintf("Provider '%s' not supported in PicoClaw, skipping", name))
				}
				continue
			}

			pc := config.ProviderConfig{APIKey: apiKey, APIBase: apiBase}
			switch name {
			case "anthropic":
				cfg.Providers.Anthropic = pc
			case "openai":
				cfg.Providers.OpenAI = pc
			case "openrouter":
				cfg.Providers.OpenRouter = pc
			case "groq":
				cfg.Providers.Groq = pc
			case "zhipu":
				cfg.Providers.Zhipu = pc
			case "vllm":
				cfg.Providers.VLLM = pc
			case "gemini":
				cfg.Providers.Gemini = pc
			}
		}
	}

	if channels, ok := getMap(data, "channels"); ok {
		for name, val := range channels {
			cMap, ok := val.(map[string]interface{})
			if !ok {
				continue
			}
			if !supportedChannels[name] {
				warnings = append(warnings, fmt.Sprintf("Channel '%s' not supported in PicoClaw, skipping", name))
				continue
			}
			enabled, _ := getBool(cMap, "enabled")
			allowFrom := getStringSlice(cMap, "allow_from")

			switch name {
			case "telegram":
				cfg.Channels.Telegram.Enabled = enabled
				cfg.Channels.Telegram.AllowFrom = allowFrom
				if v, ok := getString(cMap, "token"); ok {
					cfg.Channels.Telegram.Token = v
				}
			case "discord":
				cfg.Channels.Discord.Enabled = enabled
				cfg.Channels.Discord.AllowFrom = allowFrom
				if v, ok := getString(cMap, "token"); ok {
					cfg.Channels.Discord.Token = v
				}
			case "whatsapp":
				cfg.Channels.WhatsApp.Enabled = enabled
				cfg.Channels.WhatsApp.AllowFrom = allowFrom
				if v, ok := getString(cMap, "bridge_url"); ok {
					cfg.Channels.WhatsApp.BridgeURL = v
				}
			case "feishu":
				cfg.Channels.Feishu.Enabled = enabled
				cfg.Channels.Feishu.AllowFrom = allowFrom
				if v, ok := getString(cMap, "app_id"); ok {
					cfg.Channels.Feishu.AppID = v
				}
				if v, ok := getString(cMap, "app_secret"); ok {
					cfg.Channels.Feishu.AppSecret = v
				}
				if v, ok := getString(cMap, "encrypt_key"); ok {
					cfg.Channels.Feishu.EncryptKey = v
				}
				if v, ok := getString(cMap, "verification_token"); ok {
					cfg.Channels.Feishu.VerificationToken = v
				}
			case "qq":
				cfg.Channels.QQ.Enabled = enabled
				cfg.Channels.QQ.AllowFrom = allowFrom
				if v, ok := getString(cMap, "app_id"); ok {
					cfg.Channels.QQ.AppID = v
				}
				if v, ok := getString(cMap, "app_secret"); ok {
					cfg.Channels.QQ.AppSecret = v
				}
			case "dingtalk":
				cfg.Channels.DingTalk.Enabled = enabled
				cfg.Channels.DingTalk.AllowFrom = allowFrom
				if v, ok := getString(cMap, "client_id"); ok {
					cfg.Channels.DingTalk.ClientID = v
				}
				if v, ok := getString(cMap, "client_secret"); ok {
					cfg.Channels.DingTalk.ClientSecret = v
				}
			case "maixcam":
				cfg.Channels.MaixCam.Enabled = enabled
				cfg.Channels.MaixCam.AllowFrom = allowFrom
				if v, ok := getString(cMap, "host"); ok {
					cfg.Channels.MaixCam.Host = v
				}
				if v, ok := getFloat(cMap, "port"); ok {
					cfg.Channels.MaixCam.Port = int(v)
				}
			case "bluebubbles":
				cfg.Channels.BlueBubbles.Enabled = enabled
				cfg.Channels.BlueBubbles.AllowFrom = allowFrom
				if v, ok := getString(cMap, "server_url"); ok {
					cfg.Channels.BlueBubbles.ServerURL = v
				}
				if v, ok := getString(cMap, "password"); ok {
					cfg.Channels.BlueBubbles.Password = v
				}
				if v, ok := getString(cMap, "webhook_path"); ok {
					cfg.Channels.BlueBubbles.WebhookPath = v
				}
				if v, ok := getString(cMap, "dm_policy"); ok {
					cfg.Channels.BlueBubbles.DmPolicy = v
				}
				if v, ok := getString(cMap, "group_policy"); ok {
					cfg.Channels.BlueBubbles.GroupPolicy = v
				}
				if v := getStringSlice(cMap, "group_allow_from"); len(v) > 0 {
					cfg.Channels.BlueBubbles.GroupAllowFrom = v
				}
			}
		}
	}

	if gateway, ok := getMap(data, "gateway"); ok {
		if v, ok := getString(gateway, "host"); ok {
			cfg.Gateway.Host = v
		}
		if v, ok := getFloat(gateway, "port"); ok {
			cfg.Gateway.Port = int(v)
		}
	}

	if tools, ok := getMap(data, "tools"); ok {
		if web, ok := getMap(tools, "web"); ok {
			// Migrate old "search" config to "brave" if api_key is present
			if search, ok := getMap(web, "search"); ok {
				if v, ok := getString(search, "api_key"); ok {
					cfg.Tools.Web.Brave.APIKey = v
					if v != "" {
						cfg.Tools.Web.Brave.Enabled = true
					}
				}
				if v, ok := getFloat(search, "max_results"); ok {
					cfg.Tools.Web.Brave.MaxResults = int(v)
					cfg.Tools.Web.DuckDuckGo.MaxResults = int(v)
				}
			}
		}
	}

	// Migrate OpenClaw voice-call plugin config into PicoClaw voice_calls.
	// OpenClaw format: plugins.entries["voice-call"] = { enabled, config: { ... } }
	if plugins, ok := getMap(data, "plugins"); ok {
		if entries, ok := getMap(plugins, "entries"); ok {
			if voiceCallEntry, ok := getMap(entries, "voice-call"); ok {
				entryEnabled, _ := getBool(voiceCallEntry, "enabled")
				if voiceCallConfig, ok := getMap(voiceCallEntry, "config"); ok {
					cfgEnabled, _ := getBool(voiceCallConfig, "enabled")
					cfg.VoiceCalls.Enabled = entryEnabled || cfgEnabled

					if v, ok := getString(voiceCallConfig, "provider"); ok {
						cfg.VoiceCalls.Provider = v
					}
					if v, ok := getString(voiceCallConfig, "public_url"); ok {
						cfg.VoiceCalls.PublicURL = v
					}
					if v, ok := getString(voiceCallConfig, "webhook_path"); ok {
						cfg.VoiceCalls.WebhookPath = v
					}
					// OpenClaw uses stream_path or streaming.stream_path depending on version.
					if v, ok := getString(voiceCallConfig, "stream_path"); ok {
						cfg.VoiceCalls.StreamPath = v
					}
					if v, ok := getString(voiceCallConfig, "from_number"); ok {
						cfg.VoiceCalls.FromNumber = v
					}
					// OpenClaw uses to_number; PicoClaw uses default_to_number.
					if v, ok := getString(voiceCallConfig, "to_number"); ok {
						cfg.VoiceCalls.DefaultToNumber = v
					}
					if v, ok := getString(voiceCallConfig, "inbound_policy"); ok {
						cfg.VoiceCalls.InboundPolicy = v
					}
					if v := getStringSlice(voiceCallConfig, "allow_from"); len(v) > 0 {
						cfg.VoiceCalls.AllowFrom = v
					}
					if v, ok := getBool(voiceCallConfig, "skip_signature_verification"); ok {
						cfg.VoiceCalls.SkipSignatureVerification = v
					}
					// OpenClaw uses store (file path); PicoClaw uses store_dir.
					if v, ok := getString(voiceCallConfig, "store"); ok {
						cfg.VoiceCalls.StoreDir = v
					}

					if v, ok := getString(voiceCallConfig, "elevenlabs_agent_id"); ok {
						cfg.VoiceCalls.ElevenLabsAgentID = v
					}
					if v, ok := getString(voiceCallConfig, "elevenlabs_phone_number_id"); ok {
						cfg.VoiceCalls.ElevenLabsPhoneNumberID = v
					}

					if twilio, ok := getMap(voiceCallConfig, "twilio"); ok {
						if v, ok := getString(twilio, "account_sid"); ok {
							cfg.VoiceCalls.Twilio.AccountSID = v
						}
						if v, ok := getString(twilio, "auth_token"); ok {
							cfg.VoiceCalls.Twilio.AuthToken = v
						}
					}

					if tts, ok := getMap(voiceCallConfig, "tts"); ok {
						if v, ok := getString(tts, "provider"); ok {
							cfg.VoiceCalls.TTS.Provider = v
						}
						if el, ok := getMap(tts, "elevenlabs"); ok {
							if v, ok := getString(el, "api_key"); ok {
								cfg.VoiceCalls.TTS.ElevenLabs.APIKey = v
							}
							if v, ok := getString(el, "base_url"); ok {
								cfg.VoiceCalls.TTS.ElevenLabs.BaseURL = v
							}
							if v, ok := getString(el, "voice_id"); ok {
								cfg.VoiceCalls.TTS.ElevenLabs.VoiceID = v
							}
							if v, ok := getString(el, "model_id"); ok {
								cfg.VoiceCalls.TTS.ElevenLabs.ModelID = v
							}
						}
					}

					if streaming, ok := getMap(voiceCallConfig, "streaming"); ok {
						if v, ok := getBool(streaming, "enabled"); ok {
							cfg.VoiceCalls.Streaming.Enabled = v
						}
						if v, ok := getString(streaming, "stt_provider"); ok {
							cfg.VoiceCalls.Streaming.STTProvider = v
						}
						if v, ok := getString(streaming, "stream_path"); ok {
							cfg.VoiceCalls.Streaming.StreamPath = v
							// Keep top-level stream_path in sync when it isn't explicitly set.
							if cfg.VoiceCalls.StreamPath == "" {
								cfg.VoiceCalls.StreamPath = v
							}
						}
						if v, ok := getString(streaming, "openai_api_key"); ok {
							cfg.VoiceCalls.Streaming.OpenAIAPIKey = v
						}
						if v, ok := getString(streaming, "stt_model"); ok {
							cfg.VoiceCalls.Streaming.STTModel = v
						}
					}
				} else if entryEnabled {
					cfg.VoiceCalls.Enabled = true
				}
			}
		}
	}

	return cfg, warnings, nil
}

func MergeConfig(existing, incoming *config.Config) *config.Config {
	if existing.Providers.Anthropic.APIKey == "" {
		existing.Providers.Anthropic = incoming.Providers.Anthropic
	}
	if existing.Providers.OpenAI.APIKey == "" {
		existing.Providers.OpenAI = incoming.Providers.OpenAI
	}
	if existing.Providers.OpenRouter.APIKey == "" {
		existing.Providers.OpenRouter = incoming.Providers.OpenRouter
	}
	if existing.Providers.Groq.APIKey == "" {
		existing.Providers.Groq = incoming.Providers.Groq
	}
	if existing.Providers.Zhipu.APIKey == "" {
		existing.Providers.Zhipu = incoming.Providers.Zhipu
	}
	if existing.Providers.VLLM.APIKey == "" && existing.Providers.VLLM.APIBase == "" {
		existing.Providers.VLLM = incoming.Providers.VLLM
	}
	if existing.Providers.Gemini.APIKey == "" {
		existing.Providers.Gemini = incoming.Providers.Gemini
	}

	if !existing.Channels.Telegram.Enabled && incoming.Channels.Telegram.Enabled {
		existing.Channels.Telegram = incoming.Channels.Telegram
	}
	if !existing.Channels.Discord.Enabled && incoming.Channels.Discord.Enabled {
		existing.Channels.Discord = incoming.Channels.Discord
	}
	if !existing.Channels.WhatsApp.Enabled && incoming.Channels.WhatsApp.Enabled {
		existing.Channels.WhatsApp = incoming.Channels.WhatsApp
	}
	if !existing.Channels.Feishu.Enabled && incoming.Channels.Feishu.Enabled {
		existing.Channels.Feishu = incoming.Channels.Feishu
	}
	if !existing.Channels.QQ.Enabled && incoming.Channels.QQ.Enabled {
		existing.Channels.QQ = incoming.Channels.QQ
	}
	if !existing.Channels.DingTalk.Enabled && incoming.Channels.DingTalk.Enabled {
		existing.Channels.DingTalk = incoming.Channels.DingTalk
	}
	if !existing.Channels.MaixCam.Enabled && incoming.Channels.MaixCam.Enabled {
		existing.Channels.MaixCam = incoming.Channels.MaixCam
	}

	if existing.Tools.Web.Brave.APIKey == "" {
		existing.Tools.Web.Brave = incoming.Tools.Web.Brave
	}

	return existing
}

func camelToSnake(s string) string {
	var result strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				prev := rune(s[i-1])
				if unicode.IsLower(prev) || unicode.IsDigit(prev) {
					result.WriteRune('_')
				} else if unicode.IsUpper(prev) && i+1 < len(s) && unicode.IsLower(rune(s[i+1])) {
					result.WriteRune('_')
				}
			}
			result.WriteRune(unicode.ToLower(r))
		} else {
			result.WriteRune(r)
		}
	}
	return result.String()
}

func convertKeysToSnake(data interface{}) interface{} {
	switch v := data.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{}, len(v))
		for key, val := range v {
			result[camelToSnake(key)] = convertKeysToSnake(val)
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(v))
		for i, val := range v {
			result[i] = convertKeysToSnake(val)
		}
		return result
	default:
		return data
	}
}

func rewriteWorkspacePath(path string) string {
	path = strings.Replace(path, ".openclaw", ".picoclaw", 1)
	return path
}

func getMap(data map[string]interface{}, key string) (map[string]interface{}, bool) {
	v, ok := data[key]
	if !ok {
		return nil, false
	}
	m, ok := v.(map[string]interface{})
	return m, ok
}

func getString(data map[string]interface{}, key string) (string, bool) {
	v, ok := data[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func getFloat(data map[string]interface{}, key string) (float64, bool) {
	v, ok := data[key]
	if !ok {
		return 0, false
	}
	f, ok := v.(float64)
	return f, ok
}

func getBool(data map[string]interface{}, key string) (bool, bool) {
	v, ok := data[key]
	if !ok {
		return false, false
	}
	b, ok := v.(bool)
	return b, ok
}

func getStringSlice(data map[string]interface{}, key string) []string {
	v, ok := data[key]
	if !ok {
		return []string{}
	}
	arr, ok := v.([]interface{})
	if !ok {
		return []string{}
	}
	result := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}
