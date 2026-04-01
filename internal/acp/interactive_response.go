package acp

import "encoding/json"

type QuestionAnswer struct {
	QuestionID        string   `json:"questionId"`
	SelectedOptionIDs []string `json:"selectedOptionIds"`
}

func BuildCursorAskQuestionAnsweredResponse(answers []QuestionAnswer) json.RawMessage {
	return mustMarshalRaw(map[string]any{
		"outcome": map[string]any{
			"outcome": "answered",
			"answers": answers,
		},
	})
}

func BuildCursorCreatePlanAcceptedResponse(planURI string) json.RawMessage {
	return mustMarshalRaw(map[string]any{
		"outcome": map[string]any{
			"outcome": "accepted",
			"planUri": planURI,
		},
	})
}

func BuildCursorCreatePlanRejectedResponse(reason string) json.RawMessage {
	return mustMarshalRaw(map[string]any{
		"outcome": map[string]any{
			"outcome": "rejected",
			"reason":  reason,
		},
	})
}

func mustMarshalRaw(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}
