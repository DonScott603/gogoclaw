package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func brokerWithSkill(name string, perms Permissions) *CapabilityBroker {
	b := NewCapabilityBroker()
	b.RegisterSkill(&SkillEntry{
		Manifest: &Manifest{
			Name:        name,
			Version:     "1.0.0",
			Description: "test",
			Permissions: perms,
		},
	})
	return b
}

func TestBrokerFileReadAllowed(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "data.txt")
	if err := os.WriteFile(testFile, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	b := brokerWithSkill("reader", Permissions{
		Filesystem: FilesystemPerms{ReadPaths: []string{dir}},
	})

	data, err := b.FileRead("reader", testFile)
	if err != nil {
		t.Fatalf("FileRead: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("data = %q, want %q", string(data), "hello")
	}
}

func TestBrokerFileReadDeniedNoPerms(t *testing.T) {
	b := brokerWithSkill("noreader", Permissions{})

	_, err := b.FileRead("noreader", "/etc/passwd")
	if err == nil {
		t.Fatal("expected error for read with no read_paths")
	}
}

func TestBrokerFileReadDeniedWrongPath(t *testing.T) {
	b := brokerWithSkill("reader", Permissions{
		Filesystem: FilesystemPerms{ReadPaths: []string{"/allowed/dir"}},
	})

	_, err := b.FileRead("reader", "/other/dir/file.txt")
	if err == nil {
		t.Fatal("expected error for read outside allowed path")
	}
}

func TestBrokerFileWriteAllowed(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "output.txt")

	b := brokerWithSkill("writer", Permissions{
		Filesystem: FilesystemPerms{WritePaths: []string{dir}},
	})

	if err := b.FileWrite("writer", testFile, []byte("written")); err != nil {
		t.Fatalf("FileWrite: %v", err)
	}

	data, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "written" {
		t.Errorf("data = %q, want %q", string(data), "written")
	}
}

func TestBrokerFileWriteDenied(t *testing.T) {
	b := brokerWithSkill("nowriter", Permissions{})

	err := b.FileWrite("nowriter", "/tmp/test.txt", []byte("data"))
	if err == nil {
		t.Fatal("expected error for write with no write_paths")
	}
}

func TestBrokerFileWriteExceedsMaxSize(t *testing.T) {
	dir := t.TempDir()

	b := brokerWithSkill("writer", Permissions{
		Filesystem: FilesystemPerms{WritePaths: []string{dir}},
		MaxFileSize: 10,
	})

	err := b.FileWrite("writer", filepath.Join(dir, "big.txt"), []byte("this is more than ten bytes"))
	if err == nil {
		t.Fatal("expected error for exceeding max_file_size")
	}
}

func TestBrokerNetworkAllowed(t *testing.T) {
	b := brokerWithSkill("netskill", Permissions{
		Network: NetworkPerms{Allowed: true, Domains: []string{"api.example.com"}},
	})

	if err := b.CheckNetwork("netskill", "api.example.com"); err != nil {
		t.Fatalf("CheckNetwork: %v", err)
	}
}

func TestBrokerNetworkDeniedNotAllowed(t *testing.T) {
	b := brokerWithSkill("nonet", Permissions{
		Network: NetworkPerms{Allowed: false},
	})

	if err := b.CheckNetwork("nonet", "example.com"); err == nil {
		t.Fatal("expected error for network not allowed")
	}
}

func TestBrokerNetworkDeniedWrongDomain(t *testing.T) {
	b := brokerWithSkill("netskill", Permissions{
		Network: NetworkPerms{Allowed: true, Domains: []string{"api.example.com"}},
	})

	if err := b.CheckNetwork("netskill", "evil.com"); err == nil {
		t.Fatal("expected error for wrong domain")
	}
}

func TestBrokerNetworkAllDomainsWhenEmpty(t *testing.T) {
	b := brokerWithSkill("opennet", Permissions{
		Network: NetworkPerms{Allowed: true, Domains: nil},
	})

	if err := b.CheckNetwork("opennet", "any-domain.com"); err != nil {
		t.Fatalf("CheckNetwork: %v (empty domains should allow all)", err)
	}
}

func TestBrokerEnvVarAllowed(t *testing.T) {
	b := brokerWithSkill("envskill", Permissions{
		EnvVars: []string{"HOME", "PATH"},
	})

	if err := b.CheckEnvVar("envskill", "HOME"); err != nil {
		t.Fatalf("CheckEnvVar: %v", err)
	}
}

func TestBrokerEnvVarDenied(t *testing.T) {
	b := brokerWithSkill("envskill", Permissions{
		EnvVars: []string{"HOME"},
	})

	if err := b.CheckEnvVar("envskill", "SECRET_KEY"); err == nil {
		t.Fatal("expected error for denied env var")
	}
}

func TestBrokerUnregisteredSkill(t *testing.T) {
	b := NewCapabilityBroker()

	_, err := b.FileRead("ghost", "/tmp/file")
	if err == nil {
		t.Fatal("expected error for unregistered skill")
	}
}

func TestBrokerLog(t *testing.T) {
	b := brokerWithSkill("logger", Permissions{})

	var logged string
	b.SetLogFunc(func(skill, msg string) {
		logged = skill + ": " + msg
	})

	b.Log("logger", "hello from skill")
	if logged != "logger: hello from skill" {
		t.Errorf("logged = %q, want %q", logged, "logger: hello from skill")
	}
}

func TestBrokerRegisterUnregister(t *testing.T) {
	b := NewCapabilityBroker()
	entry := &SkillEntry{
		Manifest: &Manifest{
			Name:        "temp",
			Version:     "1.0.0",
			Description: "test",
			Permissions: Permissions{
				Filesystem: FilesystemPerms{ReadPaths: []string{"/tmp"}},
			},
		},
	}

	b.RegisterSkill(entry)
	if err := b.checkReadPath("temp", "/tmp/file.txt"); err != nil {
		t.Fatalf("checkReadPath after register: %v", err)
	}

	b.UnregisterSkill("temp")
	if err := b.checkReadPath("temp", "/tmp/file.txt"); err == nil {
		t.Fatal("expected error after unregister")
	}
}
