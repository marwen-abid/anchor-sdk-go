package toml

import (
	"fmt"
	"net/http"
	"strings"
)

type Publisher struct {
	info *AnchorInfo
}

func NewPublisher(info *AnchorInfo) *Publisher {
	return &Publisher{info: info}
}

func (p *Publisher) Render() string {
	var b strings.Builder

	if p.info.NetworkPassphrase != "" {
		fmt.Fprintf(&b, "NETWORK_PASSPHRASE=\"%s\"\n", p.info.NetworkPassphrase)
	}
	if p.info.SigningKey != "" {
		fmt.Fprintf(&b, "SIGNING_KEY=\"%s\"\n", p.info.SigningKey)
	}
	if p.info.WebAuthEndpoint != "" {
		fmt.Fprintf(&b, "WEB_AUTH_ENDPOINT=\"%s\"\n", p.info.WebAuthEndpoint)
	}
	if p.info.TransferServerSep6 != "" {
		fmt.Fprintf(&b, "TRANSFER_SERVER=\"%s\"\n", p.info.TransferServerSep6)
	}
	if p.info.TransferServerSep24 != "" {
		fmt.Fprintf(&b, "TRANSFER_SERVER_SEP0024=\"%s\"\n", p.info.TransferServerSep24)
	}

	if len(p.info.Currencies) > 0 {
		b.WriteString("\n")
		for _, curr := range p.info.Currencies {
			b.WriteString("[[CURRENCIES]]\n")
			fmt.Fprintf(&b, "code=\"%s\"\n", curr.Code)
			if curr.Issuer != "" {
				fmt.Fprintf(&b, "issuer=\"%s\"\n", curr.Issuer)
			}
			if curr.Status != "" {
				fmt.Fprintf(&b, "status=\"%s\"\n", curr.Status)
			}
			if curr.DisplayDecimals > 0 {
				fmt.Fprintf(&b, "display_decimals=%d\n", curr.DisplayDecimals)
			}
			if curr.AnchorAssetType != "" {
				fmt.Fprintf(&b, "anchor_asset_type=\"%s\"\n", curr.AnchorAssetType)
			}
			if curr.IsAssetAnchored {
				b.WriteString("is_asset_anchored=true\n")
			}
			if curr.Desc != "" {
				fmt.Fprintf(&b, "desc=\"%s\"\n", curr.Desc)
			}
			if curr.Description != "" {
				fmt.Fprintf(&b, "description=\"%s\"\n", curr.Description)
			}
			b.WriteString("\n")
		}
	}

	return b.String()
}

func (p *Publisher) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(p.Render()))
	}
}
