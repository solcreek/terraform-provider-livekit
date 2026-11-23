// SPDX-License-Identifier: MPL-2.0

package livekit

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// PhoneNumber is a phone number a project bought, or one for sale.
type PhoneNumber struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	E164               string   `json:"e164Format"`
	CountryCode        string   `json:"countryCode"`
	AreaCode           string   `json:"areaCode"`
	NumberType         string   `json:"numberType"`
	Locality           string   `json:"locality"`
	Region             string   `json:"region"`
	Capabilities       []string `json:"capabilities"`
	Status             string   `json:"status"`
	SIPDispatchRuleIDs []string `json:"sipDispatchRuleIds"`
}

// PhoneNumberReleased is the status of a number the project gave up.
const PhoneNumberReleased = "PHONE_NUMBER_STATUS_RELEASED"

func phoneNumbersPath(projectID string) string {
	return "/v0/projects/" + seg(projectID) + "/phone-numbers"
}

type phoneNumberEnvelope struct {
	PhoneNumber PhoneNumber `json:"phoneNumber"`
}

// SearchPhoneNumbers lists numbers for sale in a country (ISO 3166-1 alpha-2),
// optionally in one area code. It is free and buys nothing.
func (c *Client) SearchPhoneNumbers(ctx context.Context, projectID, countryCode, areaCode string, limit int) ([]PhoneNumber, error) {
	q := url.Values{"countryCode": {countryCode}}
	if areaCode != "" {
		q.Set("areaCode", areaCode)
	}
	if limit > 0 {
		q.Set("page.pageSize", itoa(limit))
	}
	var out struct {
		Items []PhoneNumber `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, phoneNumbersPath(projectID)+":search", q, nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// PurchasePhoneNumber BUYS a number for the project, which costs money. Its
// response is not complete (observed 2026-11-23): use FindPhoneNumber.
func (c *Client) PurchasePhoneNumber(ctx context.Context, projectID, e164 string) (*PhoneNumber, error) {
	var out phoneNumberEnvelope
	if err := c.do(ctx, http.MethodPost, phoneNumbersPath(projectID), nil, map[string]any{"phoneNumber": e164}, &out); err != nil {
		return nil, err
	}
	return &out.PhoneNumber, nil
}

// GetPhoneNumber returns one of the project's numbers.
func (c *Client) GetPhoneNumber(ctx context.Context, projectID, id string) (*PhoneNumber, error) {
	var out phoneNumberEnvelope
	if err := c.do(ctx, http.MethodGet, phoneNumbersPath(projectID)+"/"+seg(id), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out.PhoneNumber, nil
}

// RenamePhoneNumber changes a number's name. Its response is not complete
// (observed 2026-11-23), so it is not returned: read the number back. A
// rename is idempotent, so a server error is retried: one came back when a
// number was renamed right after it was bought.
func (c *Client) RenamePhoneNumber(ctx context.Context, projectID, id, name string) error {
	var err error
	for attempt := 0; attempt <= maxNetworkRetries; attempt++ {
		if attempt > 0 {
			if err := c.sleep(ctx, time.Duration(1<<attempt)*c.retryBase); err != nil {
				return err
			}
		}
		err = c.do(ctx, http.MethodPatch, phoneNumbersPath(projectID)+"/"+seg(id), nil, map[string]any{"name": name}, nil)
		if StatusCode(err) < 500 {
			return err
		}
	}
	return err
}

// ReleasePhoneNumber gives a number up. The project loses it for good.
func (c *Client) ReleasePhoneNumber(ctx context.Context, projectID, id string) error {
	return c.do(ctx, http.MethodDelete, phoneNumbersPath(projectID)+"/"+seg(id), nil, nil, nil)
}

// numberStatuses are the statuses a number the project holds can have.
var numberStatuses = []string{"PHONE_NUMBER_STATUS_ACTIVE", "PHONE_NUMBER_STATUS_PENDING", "PHONE_NUMBER_STATUS_OFFLINE"}

// FindPhoneNumber finds a number the project holds by its E.164 form. A
// number it does not hold is a 404.
func (c *Client) FindPhoneNumber(ctx context.Context, projectID, e164 string) (*PhoneNumber, error) {
	all, err := list[PhoneNumber](ctx, c, phoneNumbersPath(projectID), url.Values{"statuses": numberStatuses})
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].E164 == e164 {
			return &all[i], nil
		}
	}
	return nil, &APIError{StatusCode: http.StatusNotFound, Code: 5, Message: "phone number not found"}
}
