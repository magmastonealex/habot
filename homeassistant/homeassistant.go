package homeassistant

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"
)

type HaEntity struct {
	Attributes struct {
		AssumedState bool     `json:"assumed_state"`
		Auto         bool     `json:"auto"`
		EntityID     []string `json:"entity_id"`
		FriendlyName string   `json:"friendly_name"`
		Hidden       bool     `json:"hidden"`
		Order        int      `json:"order"`
	} `json:"attributes"`
	Context struct {
		ID     string      `json:"id"`
		UserID interface{} `json:"user_id"`
	} `json:"context"`
	EntityID    string    `json:"entity_id"`
	LastChanged time.Time `json:"last_changed"`
	LastUpdated time.Time `json:"last_updated"`
	State       string    `json:"state"`
}

type ServiceInvocation struct {
	EntityID   string `json:"entity_id,omitempty"`
	Brightness string `json:"brightness,omitempty"`
}

type HaInvoke struct {
	Domain  string            `json:"domain"`
	Service string            `json:"service"`
	Data    ServiceInvocation `json:"data"`
}

type GroupEntity struct {
	FriendlyName string
	EntityID     string
	SubEntities  []SummarizedEntity
}
type SummarizedEntity struct {
	EntityID     string
	State        string
	FriendlyName string
}

type HomeAssistant struct {
	BaseUrl string // Similar to http://127.0.0.1:8123
	ApiKey  string // currently not used
}

func (ha HomeAssistant) FetchStateFromHA(entId string) (HaEntity, error) {
	requestUrl := fmt.Sprintf("%s/api/states/%s", ha.BaseUrl, entId)

	resp, err := http.Get(requestUrl)
	if err != nil {
		return HaEntity{}, err
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return HaEntity{}, err
	}

	response := HaEntity{}
	if err = json.Unmarshal(body, &response); err != nil {
		return HaEntity{}, err
	}

	return response, nil

}

func (ha HomeAssistant) GetEntityForId(id string) (SummarizedEntity, error) {
	response, err := ha.FetchStateFromHA(id)
	if err != nil {
		return SummarizedEntity{}, err
	}

	return SummarizedEntity{
		EntityID:     response.EntityID,
		FriendlyName: response.Attributes.FriendlyName,
		State:        response.State,
	}, nil
}

func (ha HomeAssistant) GetEntitiesForGroup(group string) (GroupEntity, error) {
	response, err := ha.FetchStateFromHA(group)
	if err != nil {
		return GroupEntity{}, err
	}

	returnedInfo := GroupEntity{
		EntityID:     group,
		FriendlyName: response.Attributes.FriendlyName,
	}

	summarized := make([]SummarizedEntity, len(response.Attributes.EntityID))
	for idx, id := range response.Attributes.EntityID {
		summarized[idx], err = ha.GetEntityForId(id)
		if err != nil {
			return GroupEntity{}, err
		}
	}

	returnedInfo.SubEntities = summarized
	return returnedInfo, nil
}

func (ha HomeAssistant) InvokeService(data HaInvoke) error {
	requestUrl := fmt.Sprintf("%s/api/services/%s/%s", ha.BaseUrl, data.Domain, data.Service)

	body, err := json.Marshal(data.Data)
	if err != nil {
		return err
	}

	resp, err := http.Post(requestUrl, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	if resp.StatusCode == 200 {
		return nil
	} else {
		return errors.New(fmt.Sprintf("Bad status code: %s", resp.Status))
	}
}
