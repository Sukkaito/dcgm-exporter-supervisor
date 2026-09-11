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
	"testing"
)

func TestParseDomainXML(t *testing.T) {
	xmlWithGPUAndVsock := `<domain type='kvm'>
  <name>gpu-worker-1</name>
  <uuid>c7a5fdb1-c034-47a3-a740-4284b067d581</uuid>
  <devices>
    <hostdev mode='subsystem' type='pci' managed='yes'>
      <source>
        <address domain='0x0000' bus='0x01' slot='0x00' function='0x0'/>
      </source>
    </hostdev>
    <vsock model='virtio'>
      <cid auto='no' address='3'/>
    </vsock>
  </devices>
</domain>`

	dom, err := ParseDomainXML([]byte(xmlWithGPUAndVsock))
	if err != nil {
		t.Fatalf("unexpected error parsing domain XML: %v", err)
	}

	if dom.Name != "gpu-worker-1" {
		t.Errorf("expected name gpu-worker-1, got %s", dom.Name)
	}
	if dom.UUID != "c7a5fdb1-c034-47a3-a740-4284b067d581" {
		t.Errorf("expected UUID c7a5fdb1-c034-47a3-a740-4284b067d581, got %s", dom.UUID)
	}
	if !dom.HasGPU() {
		t.Errorf("expected HasGPU to be true")
	}
	if cid := dom.GetVsockCID(); cid != "3" {
		t.Errorf("expected VSOCK CID 3, got %s", cid)
	}

	xmlNonGPU := `<domain type='kvm'>
  <name>web-server</name>
  <uuid>d8b6fdb2-d123-47a3-b840-4284b067d582</uuid>
  <devices>
    <disk type='file' device='disk'/>
  </devices>
</domain>`

	domNonGPU, err := ParseDomainXML([]byte(xmlNonGPU))
	if err != nil {
		t.Fatalf("unexpected error parsing non-GPU domain XML: %v", err)
	}
	if domNonGPU.HasGPU() {
		t.Errorf("expected HasGPU to be false for non-GPU domain")
	}
}

func TestParseDomainXML_Tenant(t *testing.T) {
	// Nova instance with owner project
	xmlNovaOwner := `<domain type='kvm'>
  <name>instance-00000001</name>
  <uuid>c7a5fdb1-c034-47a3-a740-4284b067d581</uuid>
  <metadata>
    <nova:instance xmlns:nova="http://openstack.org/xmlns/libvirt/nova/1.0">
      <nova:owner>
        <nova:project uuid="tenant-uuid-1234">tenant-alpha</nova:project>
      </nova:owner>
    </nova:instance>
  </metadata>
</domain>`

	dom, err := ParseDomainXML([]byte(xmlNovaOwner))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	uuid, name := dom.GetTenant()
	if uuid != "tenant-uuid-1234" || name != "tenant-alpha" {
		t.Errorf("expected tenant-uuid-1234 / tenant-alpha, got uuid=%s name=%s", uuid, name)
	}

	// Title fallback
	xmlTitle := `<domain type='kvm'>
  <name>custom-vm</name>
  <uuid>c7a5fdb1-c034-47a3-a740-4284b067d582</uuid>
  <title>tenant:tenant-beta</title>
</domain>`
	domTitle, err := ParseDomainXML([]byte(xmlTitle))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	uuid2, _ := domTitle.GetTenant()
	if uuid2 != "tenant-beta" {
		t.Errorf("expected tenant-beta, got %s", uuid2)
	}
}
