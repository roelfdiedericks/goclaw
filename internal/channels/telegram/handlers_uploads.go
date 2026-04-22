package telegram

import (
	"fmt"
	"io"

	tele "gopkg.in/telebot.v4"

	"github.com/roelfdiedericks/goclaw/internal/gateway"
	"github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/media"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// processUpload is the generic handler for any non-photo/non-voice Telegram
// upload (documents, videos, audio files, animations, video notes, stickers).
// It mirrors handlePhoto/handleVoice in structure (auth, group-chat bailout,
// typing indicator, streamed response) and delegates block construction to
// media.BuildUploadBlock.
//
// mediaType is the cosmetic media-store subdir label ("document", "video",
// "audio", "animation", "video_note", "sticker").
func (b *Bot) processUpload(
	c tele.Context,
	file *tele.File,
	origName string,
	providedMime string,
	mediaType string,
	duration int,
) error {
	sender := c.Sender()
	userID := fmt.Sprintf("%d", sender.ID)
	chatID := c.Chat().ID
	isGroup := c.Chat().Type != tele.ChatPrivate

	logging.L_debug("telegram upload received",
		"userID", userID,
		"chatID", chatID,
		"isGroup", isGroup,
		"mediaType", mediaType,
		"origName", origName,
		"providedMime", providedMime,
	)

	if isGroup {
		logging.L_debug("telegram: ignoring group upload", "mediaType", mediaType)
		return nil
	}

	u := b.users.FromIdentity("telegram", userID)
	if u == nil {
		logging.L_warn("telegram: unknown user ignored (upload)", "userID", userID, "mediaType", mediaType)
		return nil
	}

	logging.L_info("telegram: authenticated upload", "user", u.Name, "role", u.Role, "mediaType", mediaType)

	_ = c.Notify(tele.Typing)

	if file == nil {
		logging.L_warn("telegram: upload missing file metadata", "mediaType", mediaType)
		return nil
	}

	reader, err := b.bot.File(file)
	if err != nil {
		logging.L_error("telegram: failed to get upload file", "error", err, "mediaType", mediaType)
		return c.Send("Sorry, I couldn't download that upload.")
	}
	defer func() { _ = reader.Close() }()

	data, err := io.ReadAll(reader)
	if err != nil {
		logging.L_error("telegram: failed to read upload data", "error", err, "mediaType", mediaType)
		return c.Send("Sorry, I couldn't process that upload.")
	}

	logging.L_debug("telegram: upload downloaded", "size", len(data), "mediaType", mediaType)

	store := b.gateway.MediaStore()
	if store == nil {
		logging.L_error("telegram: no media store available, dropping upload", "mediaType", mediaType)
		return c.Send("Sorry, I can't accept uploads right now.")
	}

	block, absPath, err := media.BuildUploadBlock(store, data, media.UploadMeta{
		Channel:        "telegram",
		User:           u,
		ChannelUserID:  userID,
		ChatID:         fmt.Sprintf("%d", chatID),
		OriginalName:   origName,
		ProvidedMime:   providedMime,
		Caption:        c.Message().Caption,
		Duration:       duration,
		IsVoiceNote:    false,
		ForceMediaType: mediaType,
	})
	if err != nil {
		logging.L_error("telegram: failed to build upload block", "error", err, "mediaType", mediaType)
		return c.Send("Sorry, I couldn't save that upload.")
	}

	logging.L_debug("telegram: upload saved",
		"mediaType", mediaType,
		"blockType", block.Type,
		"mime", block.MimeType,
		"path", absPath,
	)

	caption := c.Message().Caption
	if caption == "" {
		caption = fmt.Sprintf("<media:%s>", mediaType)
	}

	prefs := b.getChatPrefs(chatID, u)

	req := gateway.AgentRequest{
		User:           u,
		Source:         "telegram",
		ChatID:         fmt.Sprintf("%d", chatID),
		IsGroup:        isGroup,
		UserMsg:        caption,
		ContentBlocks:  []types.ContentBlock{block},
		EnableThinking: prefs.ShowThinking,
		ThinkingLevel:  prefs.ThinkingLevel,
		OnMediaToSend: func(path, caption string) error {
			return b.SendPhoto(chatID, path, caption)
		},
	}

	events := make(chan gateway.AgentEvent, 100)
	go func() {
		if err := b.gateway.RunAgent(b.ctx, req, events); err != nil {
			logging.L_error("telegram agent error", "error", err, "mediaType", mediaType)
		}
	}()

	return b.streamResponse(c, events)
}

// handleDocument handles arbitrary file uploads (PDFs, DOCX, ZIPs, source
// code, etc.). Telegram routes anything that isn't a photo/audio/video/sticker
// through OnDocument.
func (b *Bot) handleDocument(c tele.Context) error {
	d := c.Message().Document
	if d == nil {
		logging.L_warn("telegram: document event without Document payload")
		return nil
	}
	return b.processUpload(c, &d.File, d.FileName, d.MIME, "document", 0)
}

// handleVideo handles video file uploads.
func (b *Bot) handleVideo(c tele.Context) error {
	v := c.Message().Video
	if v == nil {
		logging.L_warn("telegram: video event without Video payload")
		return nil
	}
	return b.processUpload(c, &v.File, v.FileName, v.MIME, "video", v.Duration)
}

// handleAudio handles music / podcast file uploads. These are NOT voice notes
// (voice notes use OnVoice). The shared helper emits a "file" block because
// IsVoiceNote is false; the agent can invoke tools on the attachment as needed.
func (b *Bot) handleAudio(c tele.Context) error {
	a := c.Message().Audio
	if a == nil {
		logging.L_warn("telegram: audio event without Audio payload")
		return nil
	}
	return b.processUpload(c, &a.File, a.FileName, a.MIME, "audio", a.Duration)
}

// handleAnimation handles GIF-style short video uploads.
func (b *Bot) handleAnimation(c tele.Context) error {
	a := c.Message().Animation
	if a == nil {
		logging.L_warn("telegram: animation event without Animation payload")
		return nil
	}
	return b.processUpload(c, &a.File, a.FileName, a.MIME, "animation", 0)
}

// handleVideoNote handles Telegram's round-video "video note" messages.
// Telegram does not populate MIME or FileName for video notes, so default
// to video/mp4 and an empty filename.
func (b *Bot) handleVideoNote(c tele.Context) error {
	vn := c.Message().VideoNote
	if vn == nil {
		logging.L_warn("telegram: video_note event without VideoNote payload")
		return nil
	}
	return b.processUpload(c, &vn.File, "", "video/mp4", "video_note", vn.Duration)
}

// handleSticker handles sticker uploads. Static stickers are WebP images,
// animated stickers are TGS (zipped Lottie JSON), video stickers are WebM.
func (b *Bot) handleSticker(c tele.Context) error {
	s := c.Message().Sticker
	if s == nil {
		logging.L_warn("telegram: sticker event without Sticker payload")
		return nil
	}
	mime := "image/webp"
	switch {
	case s.Animated:
		mime = "application/x-tgsticker"
	case s.Video:
		mime = "video/webm"
	}
	return b.processUpload(c, &s.File, "", mime, "sticker", 0)
}
