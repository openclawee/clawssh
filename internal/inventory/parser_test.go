package inventory

import "testing"

const sampleINI = `
[webservers]
web1 ansible_host=10.0.0.12 ansible_user=ubuntu ansible_ssh_private_key_file=~/.ssh/id_rsa
web2 ansible_host=10.0.0.13 ansible_user=ubuntu

[databases]
db1 ansible_host=10.0.0.15 ansible_user=postgres

[all:vars]
ansible_port=22
`

const sampleWithPassword = `
[webservers]
web1 ansible_host=10.0.0.12 ansible_user=ubuntu ansible_password=secret1
web2 ansible_host=10.0.0.13

[all:vars]
ansible_port=2222
ansible_user=ops
ansible_password=defaultpass
`

func TestParseINI_ResolveAliasAndGroup(t *testing.T) {
	inv, err := ParseINI(sampleINI)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	web1, ok := inv.ResolveAlias("web1")
	if !ok {
		t.Fatal("web1 not found")
	}
	if web1.Host != "10.0.0.12" || web1.User != "ubuntu" || web1.Port != 22 {
		t.Fatalf("unexpected web1: %+v", web1)
	}

	group := inv.ResolveGroup("webservers")
	if len(group) != 2 {
		t.Fatalf("want 2 hosts in webservers, got %d", len(group))
	}
	if group[0].Alias == group[1].Alias {
		t.Fatalf("expect distinct aliases in group result: %+v", group)
	}
}

func TestParseINI_PasswordAndDefaults(t *testing.T) {
	inv, err := ParseINI(sampleWithPassword)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	web1, ok := inv.ResolveAlias("web1")
	if !ok {
		t.Fatal("web1 not found")
	}
	if web1.Password != "secret1" {
		t.Fatalf("expected web1 password override, got %q", web1.Password)
	}
	web2, ok := inv.ResolveAlias("web2")
	if !ok {
		t.Fatal("web2 not found")
	}
	if web2.User != "ops" || web2.Port != 2222 || web2.Password != "defaultpass" {
		t.Fatalf("expected defaults on web2, got %+v", web2)
	}
}
