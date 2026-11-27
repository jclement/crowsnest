package changes

import "encoding/json"

// reflectID reads an "id" field from any hub event data by its JSON form (jobs.HubEvent has
// `json:"id"`), so this package need not import jobs.
func reflectID(data any) string {
	body, err := json.Marshal(data)
	if err != nil {
		return ""
	}
	var event struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(body, &event) != nil {
		return ""
	}
	return event.ID
}
