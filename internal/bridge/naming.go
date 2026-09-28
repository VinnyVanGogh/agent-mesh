package bridge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	emailRegex       = regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`)
	retinaRegex      = regexp.MustCompile(`(?i)@[0-9]x$`)
	cleanShotHash    = regexp.MustCompile(`_[0-9]{5,7}$`)
	monthDateRegex   = regexp.MustCompile(`(?i)(?:^|[^A-Za-z])(January|February|March|April|May|June|July|August|September|October|November|December)[-_ ]([0-9]{1,2})[-_ ]([0-9]{4})(?:[^A-Za-z0-9]|$)`)
	macShotRegex     = regexp.MustCompile(`(?i)^(Screen\s*Shot|Screenshot|CleanShot)\s+([0-9]{4})[-_.]([0-9]{1,2})[-_.]([0-9]{1,2})\s+at\s+([0-9]{1,2})[\.:]([0-9]{2})[\.:]([0-9]{2})(?:\s*(AM|PM))?`)
	nonAlphaNumRegex = regexp.MustCompile(`[^A-Za-z0-9]+`)
	multiUnderRegex  = regexp.MustCompile(`_+`)
)

var monthMap = map[string]string{
	"january":   "01",
	"february":  "02",
	"march":     "03",
	"april":     "04",
	"may":       "05",
	"june":      "06",
	"july":      "07",
	"august":    "08",
	"september": "09",
	"october":   "10",
	"november":  "11",
	"december":  "12",
}

// CleanFileName converts noisy screenshot, application, and download filenames
// into clean, lowercase, snake_case names with ISO dates where present.
func CleanFileName(raw string) string {
	ext := filepath.Ext(raw)
	base := strings.TrimSuffix(raw, ext)

	// If already a clean single-word or snake_case name without special chars or spaces, return as-is
	if isAlreadyClean(base) {
		return raw
	}

	// 1. Strip retina suffix (@2x, @3x)
	base = retinaRegex.ReplaceAllString(base, "")

	// 2. Strip CleanShot random hash/serial suffix (e.g. _013188)
	base = cleanShotHash.ReplaceAllString(base, "")

	// 3. Check for macOS Screenshot / CleanShot timestamp pattern
	// e.g. "Screenshot 2026-09-24 at 10.28.06 AM"
	if sm := macShotRegex.FindStringSubmatch(base); len(sm) > 0 {
		prefix := "screenshot"
		if strings.EqualFold(sm[1], "cleanshot") {
			prefix = "cleanshot"
		}
		year := sm[2]
		month := fmt.Sprintf("%02d", mustAtoi(sm[3]))
		day := fmt.Sprintf("%02d", mustAtoi(sm[4]))
		hour := mustAtoi(sm[5])
		min := sm[6]
		sec := sm[7]
		ampm := strings.ToUpper(sm[8])
		if ampm == "PM" && hour < 12 {
			hour += 12
		} else if ampm == "AM" && hour == 12 {
			hour = 0
		}
		timeStr := fmt.Sprintf("%02d%s%s", hour, min, sec)
		return fmt.Sprintf("%s_%s-%s-%s_%s%s", prefix, year, month, day, timeStr, strings.ToLower(ext))
	}

	// 4. Extract and normalize text date (e.g. "September-24-2026")
	dateStr := ""
	if dm := monthDateRegex.FindStringSubmatch(base); len(dm) > 0 {
		mName := strings.ToLower(dm[1])
		if mNum, ok := monthMap[mName]; ok {
			dayNum := fmt.Sprintf("%02d", mustAtoi(dm[2]))
			yearNum := dm[3]
			dateStr = fmt.Sprintf("%s-%s-%s", yearNum, mNum, dayNum)
		}
		base = monthDateRegex.ReplaceAllString(base, " ")
	}

	// 5. Strip email addresses
	base = emailRegex.ReplaceAllString(base, " ")

	// 6. Detect and extract app prefix
	appPrefix := ""
	lowerBase := strings.ToLower(base)
	if strings.Contains(lowerBase, "microsoft teams") {
		appPrefix = "teams"
		base = regexp.MustCompile(`(?i)microsoft\s+teams`).ReplaceAllString(base, " ")
	} else if strings.Contains(lowerBase, "google chrome") || strings.Contains(lowerBase, "chrome") {
		appPrefix = "chrome"
		base = regexp.MustCompile(`(?i)google\s+chrome|chrome`).ReplaceAllString(base, " ")
	} else if strings.Contains(lowerBase, "slack") {
		appPrefix = "slack"
		base = regexp.MustCompile(`(?i)slack`).ReplaceAllString(base, " ")
	} else if strings.Contains(lowerBase, "zoom") {
		appPrefix = "zoom"
		base = regexp.MustCompile(`(?i)zoom`).ReplaceAllString(base, " ")
	}

	// 7. Strip common organizational and chat boilerplate
	base = regexp.MustCompile(`(?i)\bchat\b`).ReplaceAllString(base, " ")
	base = regexp.MustCompile(`(?i)\bmanaged\s+solution\b`).ReplaceAllString(base, " ")
	base = regexp.MustCompile(`(?i)\bvince\s+vasile\s*(\(you\))?\b`).ReplaceAllString(base, " ")

	// 8. Convert non-alphanumeric chars to underscores
	slug := nonAlphaNumRegex.ReplaceAllString(base, "_")
	slug = strings.ToLower(strings.Trim(slug, "_"))

	// 9. Deduplicate repeated app prefix (e.g. teams_teams_features -> teams_features)
	if appPrefix != "" {
		if strings.HasPrefix(slug, appPrefix+"_") {
			slug = strings.TrimPrefix(slug, appPrefix+"_")
		} else if slug == appPrefix {
			slug = ""
		}
	}

	// 10. Assemble final parts: [app] [slug] [date]
	var parts []string
	if appPrefix != "" {
		parts = append(parts, appPrefix)
	}
	if slug != "" {
		parts = append(parts, slug)
	}
	if dateStr != "" {
		parts = append(parts, dateStr)
	}

	res := strings.Join(parts, "_")
	res = multiUnderRegex.ReplaceAllString(res, "_")
	res = strings.Trim(res, "_")

	if res == "" {
		res = "file"
	}

	return res + strings.ToLower(ext)
}

func isAlreadyClean(s string) bool {
	// A file is already clean if it contains no spaces, no @ symbols,
	// only lowercase letters, digits, underscores, or hyphens.
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			return false
		}
		if r == ' ' || r == '@' || r == '(' || r == ')' || r == '+' {
			return false
		}
	}
	return true
}

func mustAtoi(s string) int {
	v, _ := strconv.Atoi(s)
	return v
}

// IsOpaqueImage checks whether a filename is generic and lacks descriptive keywords.
func IsOpaqueImage(fileName string) bool {
	lower := strings.ToLower(fileName)
	ext := filepath.Ext(lower)
	if ext != ".png" && ext != ".jpg" && ext != ".jpeg" && ext != ".webp" {
		return false
	}
	base := strings.TrimSuffix(lower, ext)
	if strings.HasPrefix(base, "screenshot") ||
		strings.HasPrefix(base, "screen shot") ||
		strings.HasPrefix(base, "cleanshot") ||
		strings.HasPrefix(base, "img_") ||
		strings.HasPrefix(base, "pasted image") ||
		strings.HasPrefix(base, "image") {
		// If it has substantive words beyond timestamp words, it is not opaque
		words := nonAlphaNumRegex.Split(base, -1)
		substantive := 0
		for _, w := range words {
			if w == "" || w == "screenshot" || w == "screen" || w == "shot" ||
				w == "cleanshot" || w == "img" || w == "image" || w == "at" ||
				w == "am" || w == "pm" || isNumber(w) {
				continue
			}
			substantive++
		}
		return substantive == 0
	}
	return false
}

func isNumber(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}

// SuggestAIName uses Gemini 2.0 Flash to inspect an image file and suggest a 2-4 word snake_case label.
// It times out after 2.5 seconds and returns empty string on any failure or missing API key.
func SuggestAIName(ctx context.Context, imagePath string, apiKey string) string {
	if apiKey == "" {
		return ""
	}

	data, err := os.ReadFile(imagePath)
	if err != nil || len(data) == 0 || len(data) > 10*1024*1024 {
		return ""
	}

	mimeType := "image/png"
	switch strings.ToLower(filepath.Ext(imagePath)) {
	case ".jpg", ".jpeg":
		mimeType = "image/jpeg"
	case ".webp":
		mimeType = "image/webp"
	}

	b64Data := base64.StdEncoding.EncodeToString(data)

	reqBody := map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"parts": []map[string]interface{}{
					{
						"text": "Look at this image. Output a concise 2 to 4 word label in snake_case describing what it depicts (for example: vpn_login_error or aws_billing_chart or teams_meeting_notes). Output ONLY the snake_case label without file extension or markdown.",
					},
					{
						"inline_data": map[string]interface{}{
							"mime_type": mimeType,
							"data":      b64Data,
						},
					},
				},
			},
		},
		"generationConfig": map[string]interface{}{
			"temperature":     0.2,
			"maxOutputTokens": 20,
		},
	}

	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return ""
	}

	callCtx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()

	url := "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.0-flash:generateContent?key=" + apiKey
	httpReq, err := http.NewRequestWithContext(callCtx, "POST", url, bytes.NewReader(jsonBytes))
	if err != nil {
		return ""
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(httpReq)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}

	var parsed struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}

	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return ""
	}

	if len(parsed.Candidates) == 0 || len(parsed.Candidates[0].Content.Parts) == 0 {
		return ""
	}

	rawText := strings.TrimSpace(parsed.Candidates[0].Content.Parts[0].Text)
	rawText = strings.ReplaceAll(rawText, "`", "")
	rawText = strings.TrimSpace(rawText)

	// Sanitize output into clean snake_case
	slug := nonAlphaNumRegex.ReplaceAllString(rawText, "_")
	slug = strings.ToLower(strings.Trim(slug, "_"))
	if len(slug) > 40 {
		slug = slug[:40]
		slug = strings.TrimRight(slug, "_")
	}

	return slug
}
