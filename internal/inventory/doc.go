// Package inventory parses host inventory definitions and provides alias/group indexes.
//
// Current parser supports Ansible-INI style sections:
//   [group]
//   alias ansible_host=10.0.0.12 ansible_user=ubuntu ansible_ssh_private_key_file=~/.ssh/id_rsa
//
// And global defaults from:
//   [all:vars]
//   ansible_port=22
//
// Retrieval API:
//   - ResolveAlias("web1") -> HostInfo
//   - ResolveGroup("webservers") -> []HostInfo
package inventory

