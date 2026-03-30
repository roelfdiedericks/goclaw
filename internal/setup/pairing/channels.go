package pairing

// TelegramStartRequest starts Telegram owner pairing using a bot token.
type TelegramStartRequest struct {
	StartRequest
	BotToken string `json:"botToken"`
}

// WhatsAppStartRequest starts WhatsApp owner pairing.
type WhatsAppStartRequest struct {
	StartRequest
}
