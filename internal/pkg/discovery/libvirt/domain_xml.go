/*
 * Copyright (c) 2026, Sukkaito. All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package libvirt

import (
	"encoding/xml"
	"fmt"
	"strings"
)

// DomainXML mirrors relevant parts of Libvirt domain XML.
type DomainXML struct {
	XMLName     xml.Name       `xml:"domain"`
	Name        string         `xml:"name"`
	UUID        string         `xml:"uuid"`
	Title       string         `xml:"title"`
	Description string         `xml:"description"`
	Metadata    DomainMetadata `xml:"metadata"`
	Devices     struct {
		Hostdevs   []Hostdev `xml:"hostdev"`
		Vsock      *Vsock    `xml:"vsock"`
		Interfaces []struct {
			MAC struct {
				Address string `xml:"address,attr"`
			} `xml:"mac"`
		} `xml:"interface"`
	} `xml:"devices"`
}

type NovaProject struct {
	UUID string `xml:"uuid,attr"`
	Name string `xml:",chardata"`
}

type NovaInstance struct {
	Project *NovaProject `xml:"project"`
	Owner   struct {
		Project *NovaProject `xml:"project"`
	} `xml:"owner"`
}

type DomainMetadata struct {
	NovaInstance *NovaInstance `xml:"instance"`
}

type Hostdev struct {
	Mode string `xml:"mode,attr"`
	Type string `xml:"type,attr"`
}

type Vsock struct {
	CID struct {
		Address string `xml:"address,attr"`
		Auto    string `xml:"auto,attr"`
	} `xml:"cid"`
}

// ParseDomainXML parses a Libvirt domain XML string.
func ParseDomainXML(data []byte) (*DomainXML, error) {
	var dom DomainXML
	if err := xml.Unmarshal(data, &dom); err != nil {
		return nil, fmt.Errorf("failed to parse domain XML: %w", err)
	}
	return &dom, nil
}

// HasGPU checks whether the domain has PCI passthrough or mediated device (mdev vGPU) attached.
func (d *DomainXML) HasGPU() bool {
	for _, h := range d.Devices.Hostdevs {
		if h.Type == "pci" || h.Type == "mdev" {
			return true
		}
	}
	return false
}

// GetVsockCID returns the VSOCK CID string if present and configured.
func (d *DomainXML) GetVsockCID() string {
	if d.Devices.Vsock != nil && d.Devices.Vsock.CID.Address != "" {
		return d.Devices.Vsock.CID.Address
	}
	return ""
}

// GetTenant returns the tenant/project UUID and/or name if defined in metadata or tags.
func (d *DomainXML) GetTenant() (string, string) {
	if d.Metadata.NovaInstance != nil {
		if d.Metadata.NovaInstance.Owner.Project != nil && d.Metadata.NovaInstance.Owner.Project.UUID != "" {
			return d.Metadata.NovaInstance.Owner.Project.UUID, d.Metadata.NovaInstance.Owner.Project.Name
		}
		if d.Metadata.NovaInstance.Project != nil && d.Metadata.NovaInstance.Project.UUID != "" {
			return d.Metadata.NovaInstance.Project.UUID, d.Metadata.NovaInstance.Project.Name
		}
	}
	if strings.HasPrefix(d.Title, "tenant:") {
		return strings.TrimPrefix(d.Title, "tenant:"), ""
	}
	if strings.HasPrefix(d.Description, "tenant:") {
		return strings.TrimPrefix(d.Description, "tenant:"), ""
	}
	return "", ""
}
