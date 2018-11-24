//   Copyright 2018 Content Mine Ltd
//
//   Licensed under the Apache License, Version 2.0 (the "License");
//   you may not use this file except in compliance with the License.
//   You may obtain a copy of the License at
//
//       http://www.apache.org/licenses/LICENSE-2.0
//
//   Unless required by applicable law or agreed to in writing, software
//   distributed under the License is distributed on an "AS IS" BASIS,
//   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//   See the License for the specific language governing permissions and
//   limitations under the License.

package wikibase

import (
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// If you're trying to encode structs to properties then you should use these types
// to help guide the code in encoding things properly for the API
type ItemPropertyType string

// These are the structs to be sent as json in the data section of a wbcreateclaim call. String does not have
// one - the value is direct for string

type itemClaim struct {
	EntityType string `json:"entity-type"`
	NumericID  int    `json:"numeric-id"`
}

type quantityClaim struct {
	Amount string `json:"amount"`
	Unit   string `json:"unit"`
}

type timeDataClaim struct {
	Time          string `json:"time"`
	TimeZone      int    `json:"timezone"`
	Before        int    `json:"before"`
	After         int    `json:"after"`
	Precision     int    `json:"precision"`
	CalendarModel string `json:"calendarmodel"`
}

// Loading item and property labels from structs

func (c *Client) MapItemConfigurationByLabel(label string) error {
	labels, err := c.FetchItemIDsForLabel(label)
	if err != nil {
		return err
	}
	switch len(labels) {
	case 0:
		return fmt.Errorf("No item ID was found for %s", label)
	case 1:
		c.ItemMap[label] = ItemPropertyType(labels[0])
	default:
		return fmt.Errorf("Multiple item IDs found for %s: %v", labels, labels)
	}
	return nil
}

func (c *Client) MapPropertyAndItemConfiguration(i interface{}) error {

	t := reflect.TypeOf(i)
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)

		tag := f.Tag.Get("property")
		if len(tag) > 0 {
			parts := strings.Split(tag, ",")
			tag = parts[0]

			labels, err := c.FetchPropertyIDsForLabel(tag)
			if err != nil {
				return err
			}
			switch len(labels) {
			case 0:
				return fmt.Errorf("No property ID was found for %s", tag)
			case 1:
				c.PropertyMap[tag] = labels[0]
			default:
				return fmt.Errorf("Multiple property IDs found for %s: %v", tag, labels)
			}
		}

		tag = f.Tag.Get("item")
		if len(tag) > 0 {
			err := c.MapItemConfigurationByLabel(tag)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// Conversation functions

func stringClaimToAPIData(value string) (*string, error) {
	if len(value) == 0 {
		return nil, nil
	}
	return &value, nil
}

func itemClaimToAPIData(value ItemPropertyType) (itemClaim, error) {

	if len(value) == 0 {
		return itemClaim{}, fmt.Errorf("We expected an item ID, but got an empty string")
	}

	runes := []rune(value)
	if runes[0] != 'Q' {
		return itemClaim{}, fmt.Errorf("We expected a Q number not %s (starts with %v)", value, runes[0])
	}

	parts := strings.Split(string(value), "Q")
	if len(parts) != 2 {
		return itemClaim{}, fmt.Errorf("We expected a Q number not %s (splits as %v)", value, parts)
	}

	id, err := strconv.Atoi(parts[1])
	if err != nil {
		return itemClaim{}, err
	}

	item := itemClaim{EntityType: "item", NumericID: id}

	return item, nil
}

func quantityClaimToAPIData(value int) (quantityClaim, error) {

	quantity := quantityClaim{
		Amount: strconv.Itoa(value),
		Unit:   "1",
	}

	return quantity, nil
}

func timeDataClaimToAPIData(value string) (timeDataClaim, error) {

	time_data := timeDataClaim{
		Time:          fmt.Sprintf("+0000000%s", value),
		Precision:     11,
		CalendarModel: "http://www.wikidata.org/entity/Q1985727",
	}

	return time_data, nil
}

// Upload properties for structs

func (c *Client) createClaimOnItem(item ItemPropertyType, property_id string, encoded_data []byte) (string, error) {

	if len(item) == 0 {
		return "", fmt.Errorf("Item ID must not be an empty string.")
	}
	if len(property_id) == 0 {
		return "", fmt.Errorf("Property ID must not be an empty string.")
	}

	editToken, terr := c.GetEditingToken()
	if terr != nil {
		return "", terr
	}

	args := map[string]string{
		"action":   "wbcreateclaim",
		"token":    editToken,
		"entity":   string(item),
		"property": property_id,
		"bot":      "1",
	}
	if encoded_data == nil || len(encoded_data) == 0 {
		args["snaktype"] = "novalue"
	} else {
		args["snaktype"] = "value"
		args["value"] = string(encoded_data)
	}

	response, err := c.client.Post(args)

	if err != nil {
		return "", err
	}
	defer response.Close()

	var res claimCreateResponse
	err = json.NewDecoder(response).Decode(&res)
	if err != nil {
		return "", err
	}

	if res.Error != nil {
		return "", fmt.Errorf("Failed to process claim %s on %s with data %v: %v", property_id, item,
			string(encoded_data), res.Error)
	}

	if res.Success != 1 {
		return "", fmt.Errorf("We got an unexpected success value adding claim %s on %s with data %v: %v", property_id,
			item, string(encoded_data), res)
	}

	return res.Claim.ID, nil

}

func getDataForClaim(f reflect.StructField, value reflect.Value) ([]byte, error) {

	// now work out how to encode this. We currently support: string, int (as quantity), Time (as TimeData),
	// and ItemPropertyType (as an item). If the field is a pointer and nil we set no value, otherwise we
	// use the deference value. Everything else we just raise an error on.

	var data []byte

	full_type_name := fmt.Sprintf("%v", f.Type)

	if value.Kind() == reflect.Ptr {
		if value.IsNil() {
			return nil, nil
		} else {
			value = value.Elem()
			if full_type_name[0] != '*' {
				return nil, fmt.Errorf("We expected a pointer type: %s", full_type_name)
			}
			full_type_name = full_type_name[1:]
		}
	}

	switch full_type_name {
	case "time.Time":
		m, ok := value.Interface().(encoding.TextMarshaler)
		if !ok {
			return nil, fmt.Errorf("time.Time does not respect JSON marshalling any more.")
		}
		var err error
		data, err = m.MarshalText()
		if err != nil {
			return nil, err
		}
		claim, claim_err := timeDataClaimToAPIData(string(data))
		if claim_err != nil {
			return nil, claim_err
		}
		return json.Marshal(claim)
	case "string":
		claim, claim_err := stringClaimToAPIData(value.String())
		if claim_err != nil {
			return nil, claim_err
		}
		if claim == nil {
			// treat empty strings as no value
			return nil, nil
		}
		return json.Marshal(claim)
	case "int":
		claim, claim_err := quantityClaimToAPIData(int(value.Int()))
		if claim_err != nil {
			return nil, claim_err
		}
		return json.Marshal(claim)
	case "wikibase.ItemPropertyType":
		claim, claim_err := itemClaimToAPIData(ItemPropertyType(value.String()))
		if claim_err != nil {
			return nil, claim_err
		}
		return json.Marshal(claim)
	default:
		return nil, fmt.Errorf("Tried to upload property of unrecognised type %s", full_type_name)
	}
}
