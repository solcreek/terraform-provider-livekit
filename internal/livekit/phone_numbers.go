// SPDX-License-Identifier: MPL-2.0

package livekit

import (
	"context"
	"net/http"
	"net/url"
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

// PurchasePhoneNumber BUYS a number for the project, which costs money.
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

// RenamePhoneNumber changes a number's name.
func (c *Client) RenamePhoneNumber(ctx context.Context, projectID, id, name string) (*PhoneNumber, error) {
	var out phoneNumberEnvelope
	if err := c.do(ctx, http.MethodPatch, phoneNumbersPath(projectID)+"/"+seg(id), nil, map[string]any{"name": name}, &out); err != nil {
		return nil, err
	}
	return &out.PhoneNumber, nil
}

// ReleasePhoneNumber gives a number up. The project loses it for good.
func (c *Client) ReleasePhoneNumber(ctx context.Context, projectID, id string) error {
	return c.do(ctx, http.MethodDelete, phoneNumbersPath(projectID)+"/"+seg(id), nil, nil, nil)
}
