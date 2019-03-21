package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/magmastonealex/habot/homeassistant"
	"github.com/nlopes/slack"
	"github.com/nlopes/slack/slackevents"
	"io/ioutil"
	"net/http"
	"regexp"
	"strings"
	"time"
)

type Confirmation struct {
	UserId string
	Value  bool
}

type InFlightRequest struct {
	InitialMessageTs  string
	CompletionChannel chan Confirmation
	TextPart          slack.MsgOption
}

type ExecutableAction struct {
	ID              string                 `json:"id"`
	ChatRegexp      string                 `json:"chatRegexp"`
	Message         string                 `json:"message"`
	RequiresConfirm bool                   `json:"requiresConfirm"`
	HaSuccessInvoke homeassistant.HaInvoke `json:"haSuccessInvoke"`
	HaFailureInvoke homeassistant.HaInvoke `json:"haFailureInvoke"`
}

type ExecutableFetch struct {
	ID         string `json:"id"`
	ChatRegexp string `json:"chatRegexp"`
	Ha         string `json:"ha"`
}

type GlobalConfiguration struct {
	SlackVerifToken      string             `json:"slackVerifToken"`
	SlackBotToken        string             `json:"slackBotToken"`
	SlackChannelRestrict string             `json:"slackChannelRestrict"`
	Actions              []ExecutableAction `json:"actions"`
	ActionsMap           map[string]ExecutableAction
	Fetches              []ExecutableFetch `json:"fetches"`
	FetchesMap           map[string]ExecutableFetch
}

var flightRequests map[string]*InFlightRequest

//Ugh. I don't like this. But I also don't feel like doing the plumbing work neccessary
// to get this into every single function.
var globalConfig GlobalConfiguration
var api *slack.Client
var ha = homeassistant.HomeAssistant{
	BaseUrl: "http://10.100.0.3:9123",
}

func waitOnRequest(messageType string) {
	timer := time.NewTimer(30 * time.Second)
	cont := Confirmation{
		UserId: "Timer",
		Value:  true,
	}

	select {
	case <-timer.C:
		fmt.Printf("Timer expired %s, continuing request", messageType)
		api.UpdateMessage(globalConfig.SlackChannelRestrict, flightRequests[messageType].InitialMessageTs, flightRequests[messageType].TextPart, slack.MsgOptionAttachments(getTimeExpiredAttachment()))
	case cont = <-flightRequests[messageType].CompletionChannel:
		fmt.Printf("Completed with %d.", cont)
		if cont.Value == true {
			api.UpdateMessage(globalConfig.SlackChannelRestrict, flightRequests[messageType].InitialMessageTs, flightRequests[messageType].TextPart, slack.MsgOptionAttachments(getConfirmedByAttachment(cont.UserId)))
		} else {
			api.UpdateMessage(globalConfig.SlackChannelRestrict, flightRequests[messageType].InitialMessageTs, flightRequests[messageType].TextPart, slack.MsgOptionAttachments(getCancelledByAttachment(cont.UserId)))
		}
	}

	var err error
	if cont.Value {
		err = continueRequest(messageType)
		if err != nil {
			api.UpdateMessage(globalConfig.SlackChannelRestrict, flightRequests[messageType].InitialMessageTs, flightRequests[messageType].TextPart, slack.MsgOptionAttachments(getConfirmedByAttachment(cont.UserId), getFailureAttachment(err)))
		}
	} else {
		err = cancelRequest(messageType)
		if err != nil {
			api.UpdateMessage(globalConfig.SlackChannelRestrict, flightRequests[messageType].InitialMessageTs, flightRequests[messageType].TextPart, slack.MsgOptionAttachments(getCancelledByAttachment(cont.UserId), getFailureAttachment(err)))
		}
	}

	timer.Stop()
	delete(flightRequests, messageType)
}

func continueRequest(action string) error {
	fmt.Printf("Continuing: %s\n", action)
	return nil
}

func cancelRequest(action string) error {
	fmt.Printf("Canceling: %s\n", action)
	return nil
}

func getSuccessAttachment() slack.Attachment {
	return slack.Attachment{
		Text: ":heavy_check_mark: Ran action",
	}
}

func getFailureAttachment(err error) slack.Attachment {
	return slack.Attachment{
		Text: fmt.Sprintf(":x: Failed to execute: %s", err.Error()),
	}
}

func getConfirmationAttachment() slack.Attachment {
	return slack.Attachment{
		Text:       "You can override this action if you want",
		Fallback:   "You are unable to override this action on this device",
		CallbackID: "confirmation_1",
		Actions: []slack.AttachmentAction{
			slack.AttachmentAction{
				Name:  "confirm",
				Text:  "I object!",
				Style: "danger",
				Type:  "button",
				Value: "no",
			},
			slack.AttachmentAction{
				Name:  "confirm",
				Text:  "Go ahead",
				Type:  "button",
				Value: "yes",
			},
		},
	}
}

func getTimeExpiredAttachment() slack.Attachment {
	return slack.Attachment{
		Text: ":stopwatch: Time expired. Continuing",
	}
}

func getConfirmedByAttachment(userid string) slack.Attachment {
	return slack.Attachment{
		Text: fmt.Sprintf(":heavy_check_mark: Thanks <@%s> for confirming!", userid),
	}
}

func getCancelledByAttachment(userid string) slack.Attachment {
	return slack.Attachment{
		Text: fmt.Sprintf(":x: <@%s> cancelled!", userid),
	}
}

func getCommandedBy(ame *slackevents.AppMentionEvent) string {
	if ame == nil {
		return "An automation"
	}
	return fmt.Sprintf("<@%s>", ame.User)
}

func getDataText(about string) string {
	return fmt.Sprintf("Here's all you ever wanted to know about %s", about)
}

func startConfirmedRequest(ame *slackevents.AppMentionEvent, typ string) {
	textPart := slack.MsgOptionText(fmt.Sprintf("%s %s", getCommandedBy(ame), globalConfig.ActionsMap[typ].Message), false)
	if flightRequests[typ] != nil {
		flightRequests[typ].CompletionChannel <- Confirmation{
			UserId: ame.User,
			Value:  true,
		}
		api.PostMessage(globalConfig.SlackChannelRestrict, textPart, slack.MsgOptionText("Someone already made that request! Confirming for you...", false))
	} else {
		_, msgTs, _ := api.PostMessage(globalConfig.SlackChannelRestrict, textPart, slack.MsgOptionAttachments(getConfirmationAttachment()))
		flightRequests[typ] = &InFlightRequest{
			InitialMessageTs:  msgTs,
			CompletionChannel: make(chan Confirmation),
			TextPart:          textPart,
		}
		go waitOnRequest(typ)
	}
}

func getState(id string) {
	var ents []homeassistant.SummarizedEntity
	var err error
	var friendlyName string
	if strings.Contains(id, "group.") {
		var group homeassistant.GroupEntity
		group, err = ha.GetEntitiesForGroup(id)
		ents = group.SubEntities
		friendlyName = group.FriendlyName
	} else {
		var entity homeassistant.SummarizedEntity
		entity, err = ha.GetEntityForId(id)
		ents = make([]homeassistant.SummarizedEntity, 1)
		ents[0] = entity
		friendlyName = entity.FriendlyName
	}

	if err != nil {
		fmt.Println("[ERROR] Failed to fetch entities")
		fmt.Println(err)
		api.PostMessage(globalConfig.SlackChannelRestrict, slack.MsgOptionText("I'm sorry, I encountered an error trying to get that for you.", false))
		return
	}

	attachments := make([]slack.Attachment, len(ents))
	for idx, entity := range ents {
		attachments[idx] = slack.Attachment{
			Text: fmt.Sprintf("%s: %s", entity.FriendlyName, entity.State),
		}
	}

	api.PostMessage(globalConfig.SlackChannelRestrict, slack.MsgOptionText(getDataText(friendlyName), false), slack.MsgOptionAttachments(attachments...))
}

func mentionRouter(ame *slackevents.AppMentionEvent) error {
	for _, act := range globalConfig.Actions {
		if ok, _ := regexp.MatchString(act.ChatRegexp, ame.Text); ok {
			fmt.Printf("Matched with: %s \n", act.ChatRegexp)
			if act.RequiresConfirm {
				startConfirmedRequest(ame, act.ID)
			} else {
				err := continueRequest(act.ID)
				if err == nil {
					api.PostMessage(globalConfig.SlackChannelRestrict, slack.MsgOptionText(act.Message, false))
				} else {
					api.PostMessage(globalConfig.SlackChannelRestrict, slack.MsgOptionText(fmt.Sprintf("I'm sorry, I encountered an error: %s", err.Error()), false))
				}
			}
			return nil
		}
	}

	for _, act := range globalConfig.Fetches {
		if ok, _ := regexp.MatchString(act.ChatRegexp, ame.Text); ok {
			go getState(act.Ha)
			return nil
		}
	}

	api.PostMessage(globalConfig.SlackChannelRestrict, slack.MsgOptionText("I'm sorry, I don't know what that means", false))
	return nil
}

func actionRouter(action string) error {
	if _, ok := globalConfig.ActionsMap[action]; ok {
		if globalConfig.ActionsMap[action].RequiresConfirm {
			startConfirmedRequest(nil, action)
		} else {
			return continueRequest(action)
		}
		return nil
	} else {
		return errors.New("Unknown option")
	}
}

func main() {
	flightRequests = make(map[string]*InFlightRequest)

	jsonConfig, err := ioutil.ReadFile("config.json")
	if err != nil {
		panic("Can't read config.json!")
	}
	if err := json.Unmarshal(jsonConfig, &globalConfig); err != nil {
		panic(err)
		return
	}

	globalConfig.ActionsMap = make(map[string]ExecutableAction)
	globalConfig.FetchesMap = make(map[string]ExecutableFetch)

	for _, act := range globalConfig.Actions {
		globalConfig.ActionsMap[act.ID] = act
	}

	for _, ftch := range globalConfig.Fetches {
		globalConfig.FetchesMap[ftch.ID] = ftch
	}

	api = slack.New(globalConfig.SlackBotToken)

	http.HandleFunc("/events-endpoint", func(w http.ResponseWriter, r *http.Request) {
		buf := new(bytes.Buffer)
		buf.ReadFrom(r.Body)
		body := buf.String()
		eventsAPIEvent, e := slackevents.ParseEvent(json.RawMessage(body), slackevents.OptionVerifyToken(&slackevents.TokenComparator{VerificationToken: ""}))
		if e != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		if eventsAPIEvent.Type == slackevents.URLVerification {
			var r *slackevents.ChallengeResponse
			err := json.Unmarshal([]byte(body), &r)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
			}
			w.Header().Set("Content-Type", "text")
			w.Write([]byte(r.Challenge))
		}
		if eventsAPIEvent.Type == slackevents.CallbackEvent {
			innerEvent := eventsAPIEvent.InnerEvent
			switch ev := innerEvent.Data.(type) {
			case *slackevents.AppMentionEvent:
				{
					if ev.Channel == globalConfig.SlackChannelRestrict {
						fmt.Printf("[INFO] Got AppMentionEvent. %s from %s in %s\n", ev.Text, ev.User, ev.Channel)
						mentionRouter(ev)
					}
				}
			}
		}
		w.WriteHeader(http.StatusOK)
	})

	http.HandleFunc("/bot-interaction", func(w http.ResponseWriter, r *http.Request) {
		body := r.PostFormValue("payload")

		var decoded slack.InteractionCallback
		e := json.Unmarshal([]byte(body), &decoded)
		if e != nil {
			fmt.Printf("[INFO] Failed to decode interactions %s\n", body)
			fmt.Println(e)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		if decoded.Token != globalConfig.SlackVerifToken {
			fmt.Printf("[WARN] Slack token mismatch: %s\n", decoded.Token)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		if decoded.Type == slack.InteractionTypeInteractionMessage {
			fmt.Printf("[INFO] Reply received for %s %s %s val\n", decoded.CallbackID, decoded.MessageTs, decoded.ActionTs)
			if flightRequests["vacuum"] != nil {
				flightRequests["vacuum"].CompletionChannel <- Confirmation{
					UserId: decoded.User.ID,
					Value:  decoded.Actions[0].Value == "yes",
				}
				w.WriteHeader(http.StatusOK)
			} else {
				originalMessage := decoded.OriginalMessage
				originalMessage.Attachments = []slack.Attachment{}
				originalMessage.Text = "I'm not quite sure what you just replied to..."
				w.Header().Add("content-type", "application/json")
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(&originalMessage)
			}
			return
		} else {
			fmt.Printf("[INFO] Unknown interaction type %s\n", decoded.Type)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
	})

	fmt.Println("[INFO] Server listening")
	http.ListenAndServe(":3000", nil)
}
