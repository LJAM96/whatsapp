// mautrix-whatsapp - A Matrix-WhatsApp puppeting bridge.
// Copyright (C) 2026 Tulir Asokan
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package msgconv

import (
	"context"
	"reflect"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
	"maunium.net/go/mautrix/event"
)

func TestFormatProductPrice(t *testing.T) {
	tests := []struct {
		name     string
		amount   int64
		currency string
		want     string
	}{
		{"zero is omitted", 0, "USD", ""},
		{"no currency", 1000, "", "1.00"},
		{"dollars and cents", 1990, "USD", "USD 1.99"},
		{"large amount", 12999000, "USD", "USD 12999.00"},
		{"three decimal places", 1234567, "EUR", "EUR 1234.567"},
		{"negative", -2500, "USD", "USD -2.50"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatProductPrice(tt.amount, tt.currency); got != tt.want {
				t.Errorf("formatProductPrice(%d, %q) = %q, want %q", tt.amount, tt.currency, got, tt.want)
			}
		})
	}
}

func TestFormatProductMessageContent(t *testing.T) {
	tests := []struct {
		name string
		msg  *waE2E.ProductMessage
		want string
	}{
		{
			name: "full product with sale price, description and link",
			msg: &waE2E.ProductMessage{
				Product: &waE2E.ProductMessage_ProductSnapshot{
					Title:               proto.String("Blue T-Shirt"),
					Description:         proto.String("A very comfortable shirt"),
					CurrencyCode:        proto.String("USD"),
					PriceAmount1000:     proto.Int64(1999000),
					SalePriceAmount1000: proto.Int64(1499000),
					URL:                 proto.String("https://wa.me/product/123"),
				},
				Body:   proto.String("Check this out"),
				Footer: proto.String("Limited stock"),
			},
			want: "Product: Blue T-Shirt\nPrice: USD 1499.00 (was USD 1999.00)\nA very comfortable shirt\n[View product](https://wa.me/product/123)\nCheck this out\nLimited stock",
		},
		{
			name: "title only",
			msg: &waE2E.ProductMessage{
				Product: &waE2E.ProductMessage_ProductSnapshot{Title: proto.String("Mug")},
			},
			want: "Product: Mug",
		},
		{
			name: "signed URL fallback",
			msg: &waE2E.ProductMessage{
				Product: &waE2E.ProductMessage_ProductSnapshot{
					Title:     proto.String("Book"),
					SignedURL: proto.String("https://example.com/signed"),
				},
			},
			want: "Product: Book\n[View product](https://example.com/signed)",
		},
		{
			name: "catalog card",
			msg: &waE2E.ProductMessage{
				Catalog: &waE2E.ProductMessage_CatalogSnapshot{
					Title:       proto.String("Acme Store"),
					Description: proto.String("All our products"),
				},
			},
			want: "Catalog: Acme Store\nAll our products",
		},
		{
			name: "empty product message",
			msg:  &waE2E.ProductMessage{},
			want: "Product message",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatProductMessageContent(tt.msg); got != tt.want {
				t.Errorf("formatProductMessageContent() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProductMessageExtra(t *testing.T) {
	tests := []struct {
		name string
		msg  *waE2E.ProductMessage
		want map[string]any
	}{
		{
			name: "product metadata",
			msg: &waE2E.ProductMessage{
				Product: &waE2E.ProductMessage_ProductSnapshot{
					ProductID:       proto.String("abc123"),
					CurrencyCode:    proto.String("USD"),
					PriceAmount1000: proto.Int64(1999),
					URL:             proto.String("https://example.com/p"),
					RetailerID:      proto.String("sku-1"),
				},
				BusinessOwnerJID: proto.String("12345@s.whatsapp.net"),
			},
			want: map[string]any{
				"fi.mau.whatsapp.business_owner_jid": "12345@s.whatsapp.net",
				"fi.mau.whatsapp.product": map[string]any{
					"product_id":        "abc123",
					"currency_code":     "USD",
					"price_amount_1000": int64(1999),
					"url":               "https://example.com/p",
					"retailer_id":       "sku-1",
				},
			},
		},
		{
			name: "catalog card has no product metadata",
			msg: &waE2E.ProductMessage{
				Catalog: &waE2E.ProductMessage_CatalogSnapshot{Title: proto.String("Store")},
			},
			want: map[string]any{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := productMessageExtra(tt.msg); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("productMessageExtra() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestConvertProductMessageWithoutImage(t *testing.T) {
	mc := &MessageConverter{}
	info := &types.MessageInfo{}
	tests := []struct {
		name     string
		msg      *waE2E.ProductMessage
		wantBody string
	}{
		{
			name: "product notice",
			msg: &waE2E.ProductMessage{
				Product: &waE2E.ProductMessage_ProductSnapshot{
					Title:           proto.String("Hat"),
					CurrencyCode:    proto.String("USD"),
					PriceAmount1000: proto.Int64(2500),
				},
			},
			wantBody: "Product: Hat\nPrice: USD 2.50",
		},
		{
			name: "catalog notice",
			msg: &waE2E.ProductMessage{
				Catalog: &waE2E.ProductMessage_CatalogSnapshot{Title: proto.String("Shop")},
			},
			wantBody: "Catalog: Shop",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			part, _ := mc.convertProductMessage(context.Background(), info, tt.msg)
			if part.Content.MsgType != event.MsgNotice {
				t.Errorf("msgtype = %q, want %q", part.Content.MsgType, event.MsgNotice)
			}
			if part.Content.Body != tt.wantBody {
				t.Errorf("body = %q, want %q", part.Content.Body, tt.wantBody)
			}
			if got := part.Extra["fi.mau.whatsapp.business_message_type"]; got != "product" {
				t.Errorf("business_message_type = %v, want product", got)
			}
		})
	}
}
