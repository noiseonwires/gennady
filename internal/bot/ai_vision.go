// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"gennadium/internal/i18n"

	tgbotapi "gennadium/internal/telegram"
)

// AzureVisionAnalyzeResponse represents the response from Azure Vision API.
type AzureVisionAnalyzeResponse struct {
	ModelVersion  string `json:"modelVersion"`
	CaptionResult *struct {
		Text       string  `json:"text"`
		Confidence float64 `json:"confidence"`
	} `json:"captionResult,omitempty"`
	ReadResult *struct {
		Blocks []struct {
			Lines []struct {
				Text string `json:"text"`
			} `json:"lines"`
		} `json:"blocks"`
	} `json:"readResult,omitempty"`
	DenseCaptionsResult *struct {
		Values []struct {
			Text       string  `json:"text"`
			Confidence float64 `json:"confidence"`
		} `json:"values"`
	} `json:"denseCaptionsResult,omitempty"`
}

// AzureContentSafetyResponse represents the response from Azure AI Content Safety API.
type AzureContentSafetyResponse struct {
	CategoriesAnalysis []struct {
		Category string `json:"category"`
		Severity int    `json:"severity"`
	} `json:"categoriesAnalysis"`
}

// analyzeImageWithVision analyzes an image using Azure Vision API.
func (b *Bot) analyzeImageWithVision(imageData []byte) (string, bool, error) {
	if !b.config.AI.ContentModeration.VisionEnabled {
		return "", false, fmt.Errorf("Azure Vision is not enabled")
	}

	url := fmt.Sprintf("%s/computervision/imageanalysis:analyze?api-version=2024-02-01&features=caption,read,denseCaptions&language=en",
		strings.TrimSuffix(b.config.AI.ContentModeration.VisionEndpoint, "/"))

	client := &http.Client{Timeout: 180 * time.Second}
	res, err := b.doAPIWithRetries("azure_vision", client, 2, func() (*http.Request, []byte, error) {
		req, rerr := http.NewRequest("POST", url, bytes.NewBuffer(imageData))
		if rerr != nil {
			return nil, nil, fmt.Errorf("error creating vision request: %v", rerr)
		}
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("Ocp-Apim-Subscription-Key", b.config.AI.ContentModeration.VisionAPIKey)
		return req, nil, nil
	})
	if err != nil {
		return "", false, fmt.Errorf("error making vision request: %v", err)
	}
	body := res.Body
	if !res.IsOK() {
		b.logAPIError("azure_vision", res.StatusCode, body, nil)
		return "", false, fmt.Errorf("vision API request failed with status %d: %s", res.StatusCode, string(body))
	}

	var visionResp AzureVisionAnalyzeResponse
	err = json.Unmarshal(body, &visionResp)
	if err != nil {
		return "", false, fmt.Errorf("error unmarshaling vision response: %v", err)
	}

	var result strings.Builder
	contentSafetyFlagged := false

	if b.config.AI.ContentModeration.ContentSafetyEnabled {
		if hasInappropriateContent, details, err := b.analyzeImageContentSafety(imageData); err != nil {
			log.Printf("Error analyzing image with Content Safety API: %v", err)
		} else if hasInappropriateContent {
			contentSafetyFlagged = true
			result.WriteString(fmt.Sprintf("[Content Safety: %s] ", strings.TrimSpace(details)))
		}
	}

	if visionResp.ReadResult != nil && len(visionResp.ReadResult.Blocks) > 0 {
		result.WriteString(i18n.T("ai.vision_ocr_text"))
		for _, block := range visionResp.ReadResult.Blocks {
			for _, line := range block.Lines {
				result.WriteString(line.Text)
				result.WriteString(" ")
			}
		}
	}

	if visionResp.CaptionResult != nil && visionResp.CaptionResult.Text != "" {
		result.WriteString(i18n.Tf("ai.vision_description", visionResp.CaptionResult.Text))
	}

	resultText := strings.TrimSpace(result.String())
	if resultText == "" {
		return i18n.T("ai.vision_no_description"), contentSafetyFlagged, nil
	}

	log.Printf("Vision API analysis result: %s", resultText)
	return resultText, contentSafetyFlagged, nil
}

// analyzeImageWithOCRSpace sends image data to the OCR.space API
// (https://ocr.space/ocrapi) for OCR text extraction. It uses the multipart
// "file" upload method and the API key sent in the header, as documented.
func (b *Bot) analyzeImageWithOCRSpace(imageData []byte) (string, error) {
	apiKey := strings.TrimSpace(b.config.AI.ContentModeration.OCRSpaceAPIKey)
	if apiKey == "" {
		return "", fmt.Errorf("OCR.space API key is not configured")
	}

	endpoint := strings.TrimSpace(b.config.AI.ContentModeration.OCRSpaceURL)
	if endpoint == "" {
		endpoint = "https://api.ocr.space/parse/image"
	}

	language := strings.TrimSpace(b.config.AI.ContentModeration.OCRSpaceLanguage)
	if language == "" {
		language = "eng"
	}
	engine := b.config.AI.ContentModeration.OCRSpaceEngine
	if engine == 0 {
		engine = 2
	}

	buildRequest := func() (*http.Request, []byte, error) {
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)
		part, err := writer.CreateFormFile("file", "image.jpg")
		if err != nil {
			return nil, nil, fmt.Errorf("error building OCR.space request: %v", err)
		}
		if _, err := part.Write(imageData); err != nil {
			return nil, nil, fmt.Errorf("error writing OCR.space image: %v", err)
		}
		_ = writer.WriteField("language", language)
		_ = writer.WriteField("OCREngine", fmt.Sprintf("%d", engine))
		_ = writer.WriteField("scale", "true")
		_ = writer.WriteField("isOverlayRequired", "false")
		if err := writer.Close(); err != nil {
			return nil, nil, fmt.Errorf("error finalizing OCR.space request: %v", err)
		}
		req, err := http.NewRequest("POST", endpoint, &buf)
		if err != nil {
			return nil, nil, fmt.Errorf("error creating OCR.space request: %v", err)
		}
		req.Header.Set("Content-Type", writer.FormDataContentType())
		req.Header.Set("apikey", apiKey)
		return req, nil, nil
	}

	client := &http.Client{Timeout: 60 * time.Second}
	res, err := b.doAPIWithRetries("ocr_space", client, 2, buildRequest)
	if err != nil {
		return "", fmt.Errorf("error making OCR.space request: %v", err)
	}
	body := res.Body
	if !res.IsOK() {
		b.logAPIError("ocr_space", res.StatusCode, body, nil)
		return "", fmt.Errorf("OCR.space returned status %d: %s", res.StatusCode, string(body))
	}

	var ocrResp struct {
		ParsedResults []struct {
			ParsedText string `json:"ParsedText"`
		} `json:"ParsedResults"`
		IsErroredOnProcessing bool        `json:"IsErroredOnProcessing"`
		OCRExitCode           int         `json:"OCRExitCode"`
		ErrorMessage          interface{} `json:"ErrorMessage"`
	}
	if err := json.Unmarshal(body, &ocrResp); err != nil {
		return "", fmt.Errorf("error unmarshaling OCR.space response: %v", err)
	}
	if ocrResp.IsErroredOnProcessing {
		return "", fmt.Errorf("OCR.space processing error (exit code %d): %v", ocrResp.OCRExitCode, ocrResp.ErrorMessage)
	}

	var sb strings.Builder
	for _, r := range ocrResp.ParsedResults {
		if t := strings.TrimSpace(r.ParsedText); t != "" {
			sb.WriteString(t)
			sb.WriteString(" ")
		}
	}
	text := strings.TrimSpace(sb.String())
	if text == "" {
		return "", nil
	}

	log.Printf("OCR.space result: %s", text)
	return i18n.Tf("ai.ocr_text", text), nil
}

// testOCRSpace performs a lightweight health check against the OCR.space API
// by sending a tiny 1x1 PNG with the configured API key. It validates the
// endpoint, method and key without consuming meaningful quota. It returns the
// HTTP status code (or 0 on transport error) and an error describing any
// problem (missing key, transport failure, non-2xx status, or an OCR.space
// processing/auth error reported in the JSON body).
func (b *Bot) testOCRSpace() (int, error) {
	apiKey := strings.TrimSpace(b.config.AI.ContentModeration.OCRSpaceAPIKey)
	if apiKey == "" {
		return 0, fmt.Errorf("OCR.space API key is not configured (ocrspace_api_key)")
	}

	endpoint := strings.TrimSpace(b.config.AI.ContentModeration.OCRSpaceURL)
	if endpoint == "" {
		endpoint = "https://api.ocr.space/parse/image"
	}

	// Minimal valid 32x32 white PNG, sent as a base64 data URI. OCR.space
	// rejects 1x1 images ("E502: Corrupted PNG"), so a small real image is used.
	const healthCheckPNG = "iVBORw0KGgoAAAANSUhEUgAAACAAAAAgCAYAAABzenr0AAAAAXNSR0IArs4c6QAAAARnQU1BAACxjwv8YQUAAAAJcEhZcwAADsMAAA7DAcdvqGQAAAAtSURBVFhH7c6hAQAACMOw/f80+B0AJpVVyTyXHtcBAAAAAAAAAAAAAAAAAAAsl/rw4k5bXakAAAAASUVORK5CYII="

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	_ = writer.WriteField("base64Image", "data:image/png;base64,"+healthCheckPNG)
	_ = writer.WriteField("language", "eng")
	_ = writer.WriteField("isOverlayRequired", "false")
	if err := writer.Close(); err != nil {
		return 0, fmt.Errorf("error building OCR.space health request: %v", err)
	}

	req, err := http.NewRequest("POST", endpoint, &buf)
	if err != nil {
		return 0, fmt.Errorf("error creating OCR.space health request: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("apikey", apiKey)

	client := &http.Client{Timeout: 15 * time.Second}
	res, err := b.doAPI(apiRequest{Service: "ocr_space", Request: req, Client: client})
	if err != nil {
		return 0, err
	}
	if !res.IsOK() {
		b.logAPIError("ocr_space", res.StatusCode, res.Body, nil)
		return res.StatusCode, fmt.Errorf("OCR.space returned status %d: %s", res.StatusCode, strings.TrimSpace(string(res.Body)))
	}

	var ocrResp struct {
		OCRExitCode           int         `json:"OCRExitCode"`
		IsErroredOnProcessing bool        `json:"IsErroredOnProcessing"`
		ErrorMessage          interface{} `json:"ErrorMessage"`
	}
	if err := json.Unmarshal(res.Body, &ocrResp); err != nil {
		return res.StatusCode, fmt.Errorf("error parsing OCR.space response: %v", err)
	}
	// A valid key on a blank image returns OCRExitCode 1 with no text. An
	// invalid key / quota problem is reported via IsErroredOnProcessing.
	if ocrResp.IsErroredOnProcessing {
		return res.StatusCode, fmt.Errorf("OCR.space error (exit code %d): %v", ocrResp.OCRExitCode, ocrResp.ErrorMessage)
	}
	return res.StatusCode, nil
}

// downloadImage downloads an image from Telegram.
func (b *Bot) downloadImage(fileID string) ([]byte, error) {
	file, err := b.tg.GetFile(fileID)
	if err != nil {
		return nil, fmt.Errorf("error getting file info: %v", err)
	}

	fileURL := file.DownloadURL
	resp, err := b.httpClient(60 * time.Second).Get(fileURL)
	if err != nil {
		return nil, fmt.Errorf("error downloading file: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("error downloading file: status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading file data: %v", err)
	}

	contentType := resp.Header.Get("Content-Type")
	log.Printf("Downloaded image: size=%d bytes, content-type=%s", len(data), contentType)

	return data, nil
}

// analyzeImageContentSafety analyzes an image using Azure AI Content Safety API.
func (b *Bot) analyzeImageContentSafety(imageData []byte) (bool, string, error) {
	if !b.config.AI.ContentModeration.ContentSafetyEnabled {
		return false, "", fmt.Errorf("Azure AI Content Safety is not enabled")
	}

	url := fmt.Sprintf("%s/contentsafety/image:analyze?api-version=2024-09-01",
		strings.TrimSuffix(b.config.AI.ContentModeration.ContentSafetyEndpoint, "/"))

	imageBase64 := base64.StdEncoding.EncodeToString(imageData)

	requestBody := map[string]interface{}{
		"image": map[string]interface{}{
			"content": imageBase64,
		},
		"categories": []string{"Hate", "SelfHarm", "Sexual", "Violence"},
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return false, "", fmt.Errorf("error marshaling request: %v", err)
	}

	client := &http.Client{Timeout: 180 * time.Second}
	res, err := b.doAPIWithRetries("content_safety", client, 2, func() (*http.Request, []byte, error) {
		req, rerr := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
		if rerr != nil {
			return nil, nil, fmt.Errorf("error creating content safety request: %v", rerr)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Ocp-Apim-Subscription-Key", b.config.AI.ContentModeration.ContentSafetyAPIKey)
		return req, jsonData, nil
	})
	if err != nil {
		return false, "", fmt.Errorf("error making content safety request: %v", err)
	}
	body := res.Body
	if !res.IsOK() {
		b.logAPIError("content_safety", res.StatusCode, body, nil)
		return false, "", fmt.Errorf("content safety API request failed with status %d: %s", res.StatusCode, string(body))
	}

	var safetyResp AzureContentSafetyResponse
	err = json.Unmarshal(body, &safetyResp)
	if err != nil {
		return false, "", fmt.Errorf("error unmarshaling content safety response: %v", err)
	}

	var details string
	for _, category := range safetyResp.CategoriesAnalysis {
		// Skip categories that scored zero - only surface the parameters that
		// actually carry a non-zero severity so the recorded details stay
		// focused on what was detected.
		if category.Severity == 0 {
			continue
		}
		details += fmt.Sprintf("%s=%d ", category.Category, category.Severity)
	}

	hasInappropriateContent := false
	for _, category := range safetyResp.CategoriesAnalysis {
		if category.Severity >= 3 {
			log.Printf("⚠️ Inappropriate content detected: category=%s, severity=%d", category.Category, category.Severity)
			hasInappropriateContent = true
		}
	}

	return hasInappropriateContent, details, nil
}

// describeImageWithFallback describes an image using the descriptive analysis
// chain (Azure Vision → OCR.space) and returns the textual description plus
// whether Azure Content Safety (invoked inside the Vision step when
// content_safety_enabled) flagged it. It is used only as a fallback when a
// dedicated Content Safety call is unavailable. Returns ("", false, nil) when
// neither Vision nor OCR.space is configured.
func (b *Bot) describeImageWithFallback(imageData []byte) (string, bool, error) {
	visionEnabled := b.config.AI.ContentModeration.VisionEnabled
	ocrSpaceEnabled := b.config.AI.ContentModeration.OCRSpaceEnabled

	var desc string
	var flagged bool
	var err error

	if visionEnabled {
		desc, flagged, err = b.analyzeImageWithVision(imageData)
		if err != nil {
			log.Printf("Image analysis (Vision) error: %v", err)
		}
	}
	// Fallback order: Azure Vision → OCR.space.
	if (err != nil || !visionEnabled) && ocrSpaceEnabled && desc == "" {
		desc, err = b.analyzeImageWithOCRSpace(imageData)
		if err != nil {
			log.Printf("Image analysis (OCR.space) error: %v", err)
		}
	}
	return strings.TrimSpace(desc), flagged, err
}

// checkFirstMessageUserProfile runs a one-shot screening of a new member's
// whole public profile on their first message in a moderation chat: their name,
// bio and profile photo, plus their linked personal channel (name, description,
// photo) when present. Photos are screened with Azure Content Safety first and
// only described via Vision / OCR.space when Content Safety is unavailable or
// fails; all the gathered text is judged by the NewUserProfilePrompt AI prompt.
// A flagged photo is recorded directly; the AI verdict is recorded too. Findings
// are appended to the user's profile so the AI moderation step can take them
// into account. It is intended to be called once per user per chat and only when
// ai.content_moderation.new_user_profile_check_enabled is on. It does not
// require content_safety_enabled.
func (b *Bot) checkFirstMessageUserProfile(message *tgbotapi.Message) {
	if message == nil || message.From == nil {
		return
	}
	if !b.config.AI.ContentModeration.NewUserProfileCheckEnabled {
		return
	}

	userID := message.From.ID

	full, err := b.tg.GetChatFull(userID)
	if err != nil {
		log.Printf("New-user profile check: error fetching profile for user %d: %v", userID, err)
		return
	}

	var channel tgbotapi.ChatFull
	havePersonalChannel := false
	if full.PersonalChatID != 0 {
		if ch, cerr := b.tg.GetChatFull(full.PersonalChatID); cerr != nil {
			log.Printf("New-user profile check: error fetching personal channel %d for user %d: %v", full.PersonalChatID, userID, cerr)
		} else {
			channel = ch
			havePersonalChannel = true
		}
	}

	var findings []string

	// Assemble every piece of profile text we have so the AI prompt sees the
	// full picture: name, bio, profile-photo description, channel name,
	// description and channel-photo description.
	var sb strings.Builder
	if name := strings.TrimSpace(strings.TrimSpace(message.From.FirstName) + " " + strings.TrimSpace(message.From.LastName)); name != "" {
		sb.WriteString("Name: ")
		sb.WriteString(name)
		sb.WriteString("\n")
	}
	if message.From.Username != "" {
		sb.WriteString("Username: @")
		sb.WriteString(message.From.Username)
		sb.WriteString("\n")
	}
	if s := strings.TrimSpace(full.Bio); s != "" {
		sb.WriteString("Bio: ")
		sb.WriteString(s)
		sb.WriteString("\n")
	}

	// User profile photo.
	if full.PhotoBigFileID != "" {
		desc, flagged := b.screenProfileImage(userID, full.PhotoBigFileID, "profile photo")
		if flagged {
			findings = append(findings, i18n.Tf("profile.screening_photo_flagged", i18n.T("profile.label_user_photo")))
		}
		if desc != "" {
			sb.WriteString("Profile photo: ")
			sb.WriteString(desc)
			sb.WriteString("\n")
		}
	}

	if havePersonalChannel {
		if s := strings.TrimSpace(channel.Title); s != "" {
			sb.WriteString("Channel name: ")
			sb.WriteString(s)
			sb.WriteString("\n")
		}
		if s := strings.TrimSpace(channel.Description); s != "" {
			sb.WriteString("Channel description: ")
			sb.WriteString(s)
			sb.WriteString("\n")
		}
		if channel.PhotoBigFileID != "" {
			desc, flagged := b.screenProfileImage(userID, channel.PhotoBigFileID, "channel photo")
			if flagged {
				findings = append(findings, i18n.Tf("profile.screening_photo_flagged", i18n.T("profile.label_channel_photo")))
			}
			if desc != "" {
				sb.WriteString("Channel photo: ")
				sb.WriteString(desc)
				sb.WriteString("\n")
			}
		}
	}

	// AI analysis of the gathered text.
	if blob := strings.TrimSpace(sb.String()); blob != "" {
		if finding := b.analyzeUserProfileText(userID, blob); finding != "" {
			findings = append(findings, finding)
		}
	}

	if len(findings) == 0 {
		return
	}
	b.recordUserProfileFinding(userID, message.From, findings)
}

// screenProfileImage downloads a profile/channel photo and screens it. Azure
// Content Safety is the primary, decisive signal; Azure Vision and OCR.space are
// only used to describe the image when Content Safety is unavailable or errors.
// Returns a textual description (empty when Content Safety handled it cleanly)
// and whether the image was flagged. label is used only for logging.
func (b *Bot) screenProfileImage(userID int64, fileID, label string) (string, bool) {
	data, derr := b.downloadImage(fileID)
	if derr != nil {
		log.Printf("New-user profile check: error downloading %s for user %d: %v", label, userID, derr)
		return "", false
	}
	if b.config.AI.ContentModeration.ContentSafetyEnabled {
		flagged, details, aerr := b.analyzeImageContentSafety(data)
		if aerr == nil {
			if flagged {
				return strings.TrimSpace(details), true
			}
			return "", false
		}
		log.Printf("New-user profile check: content safety error for %s of user %d, falling back to Vision/OCR: %v", label, userID, aerr)
	}
	// Content Safety not configured or failed → describe via Vision → OCR.space.
	desc, flagged, aerr := b.describeImageWithFallback(data)
	if aerr != nil {
		log.Printf("New-user profile check: error analyzing %s for user %d: %v", label, userID, aerr)
	}
	return desc, flagged
}

// analyzeUserProfileText runs the configured NewUserProfilePrompt over the
// gathered profile text. The prompt instructs the model to reply with exactly
// "CLEAN" when nothing is concerning; any other non-empty reply is treated as a
// finding. Returns the localized finding string, or "" when clean or on error.
func (b *Bot) analyzeUserProfileText(userID int64, text string) string {
	p := b.config.AI.ContentModeration.NewUserProfilePrompt
	if strings.TrimSpace(p.System) == "" && strings.TrimSpace(p.User) == "" {
		return ""
	}
	userPrompt := applyReplacements(p.User, map[string]string{"profile_text": text})
	if !strings.Contains(p.User, "{{profile_text}}") {
		userPrompt = strings.TrimSpace(userPrompt + "\n\n" + text)
	}
	modelConfig := b.config.AI.LightModel.Get(0)
	if b.config.AI.ContentModeration.NewUserProfileUseFullModel {
		modelConfig = b.config.AI.FullModel.Get(0)
	}
	resp, err := b.callAzureOpenAIWithConfig("new_user_profile_check", userPrompt, p.System, modelConfig, 200, false)
	if err != nil {
		log.Printf("New-user profile check: AI analysis error for user %d: %v", userID, err)
		return ""
	}
	verdict := strings.TrimSpace(resp)
	if isCleanProfileVerdict(verdict) {
		return ""
	}
	return i18n.Tf("profile.screening_flagged", verdict)
}

// isCleanProfileVerdict reports whether a new-user profile verdict means
// "nothing concerning". The prompt asks the model to reply with exactly CLEAN,
// but smaller models often answer in natural language ("nothing suspicious",
// "ничего подозрительного не найдено"). Treat those as clean too so we never
// post a flag whose only content says nothing was found.
func isCleanProfileVerdict(verdict string) bool {
	v := strings.ToLower(strings.TrimSpace(verdict))
	if v == "" {
		return true
	}
	// Strip surrounding punctuation/quotes so "CLEAN." / "«clean»" still match.
	v = strings.Trim(v, " \t\r\n.,:;!?\"'«»()")
	if v == "clean" {
		return true
	}
	cleanPhrases := []string{
		"nothing suspicious", "nothing concerning", "no issues", "no concerns",
		"ничего подозрительного", "не найдено", "не обнаружено", "всё чисто", "все чисто",
	}
	for _, p := range cleanPhrases {
		if strings.Contains(v, p) {
			return true
		}
	}
	return false
}

// recordUserProfileFinding stores the new-member screening findings in the
// user's dedicated tg_profile_analysis field (one finding per line) and
// invalidates the in-memory profile cache so the moderation prompt sees the
// findings immediately. The AI-generated behavior profile is left untouched.
func (b *Bot) recordUserProfileFinding(userID int64, user *tgbotapi.User, findings []string) {
	username := ""
	if user != nil {
		username = user.Username
	}
	if err := b.db.AppendTgProfileAnalysis(userID, username, findings); err != nil {
		log.Printf("New-user profile check: error recording findings for user %d: %v", userID, err)
		return
	}
	b.invalidateProfileCache(userID)
}
