package main

import "testing"

func TestStockEncodeURL(t *testing.T) {
	got, err := stockEncodeURL("aDm8H%MdA")
	if err != nil {
		t.Fatal(err)
	}
	if got != "aDm8H%25MdA" {
		t.Fatalf("stockEncodeURL=%q", got)
	}
}

func TestStockJSField(t *testing.T) {
	src := "var ftp_cfg = { TelnetUserName:'user-telnet', TelnetPassword:'p%1', FtpEnable:0, FtpUserName:'user_ftp', FtpPassword:'rootpw', FtpPort:'21' };"
	for key, want := range map[string]string{
		"TelnetUserName": "user-telnet",
		"TelnetPassword": "p%1",
		"FtpEnable":      "0",
		"FtpUserName":    "user_ftp",
		"FtpPassword":    "rootpw",
		"FtpPort":        "21",
	} {
		got, ok := stockJSField(src, key)
		if !ok || got != want {
			t.Fatalf("%s: got=%q ok=%v want=%q", key, got, ok, want)
		}
	}
}

func TestStockPKCS7(t *testing.T) {
	got := stockPKCS7([]byte("123456789012345"), 16)
	if len(got) != 16 || got[15] != 1 {
		t.Fatalf("bad PKCS7: len=%d tail=%d", len(got), got[len(got)-1])
	}
	got = stockPKCS7([]byte("1234567890123456"), 16)
	if len(got) != 32 || got[31] != 16 {
		t.Fatalf("full-block PKCS7: len=%d tail=%d", len(got), got[len(got)-1])
	}
}

func TestStockLoginPlanAllowsTelnetOnlyWhenFTPDisabled(t *testing.T) {
	plan, err := stockLoginPlan("XG-040G-MF", stockCredentials{
		TelnetUser: "user-telnet", TelnetPassword: "telnet-pass",
		FTPEnabled: false,
	}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if plan.LoginUser != "user-telnet" || plan.LoginPassword != "telnet-pass" {
		t.Fatalf("bad Telnet plan: %+v", plan)
	}
	if plan.RootUser != "" || plan.RootPassword != "" || plan.FTPEnabled {
		t.Fatalf("passive plan incorrectly requires FTP credentials: %+v", plan)
	}
}

func TestStockLoginPlanRequiresFTPCredentialsAfterProvision(t *testing.T) {
	_, err := stockLoginPlan("XG-040G-MF", stockCredentials{
		TelnetUser: "user-telnet", TelnetPassword: "telnet-pass",
		FTPEnabled: true,
	}, true, true)
	if err == nil {
		t.Fatal("provisioned plan accepted without FTP service credentials")
	}
}
