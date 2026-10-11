package tests

import (
	"reflect"
	"testing"

	"mako/internal/worker/runtime/androidstudio"
)

func TestBuildLaunchArgs(t *testing.T) {
	args := androidstudio.BuildLaunchArgs(androidstudio.LaunchRequest{
		AVDName:     "Medium_Phone",
		ConsolePort: 5600,
		AdbPort:     5601,
		GRPCPort:    8554,
		Headless:    true,
		ReadOnly:    true,
		WipeData:    false,
		EnableGRPC:  true,
	})

	expected := []string{
		"-avd", "Medium_Phone",
		"-ports", "5600,5601",
		"-no-window",
		"-read-only",
		"-grpc", "8554",
	}

	if !reflect.DeepEqual(args, expected) {
		t.Fatalf("unexpected args: %#v", args)
	}
}

func TestBuildLaunchArgsIncludesOptionalFlags(t *testing.T) {
	args := androidstudio.BuildLaunchArgs(androidstudio.LaunchRequest{
		AVDName:     "Tablet_API_35",
		ConsolePort: 5610,
		AdbPort:     5611,
		GRPCPort:    8555,
		Headless:    false,
		ReadOnly:    false,
		WipeData:    true,
		EnableGRPC:  false,
		ExtraArgs:   []string{"-gpu", "swiftshader_indirect"},
	})

	expected := []string{
		"-avd", "Tablet_API_35",
		"-ports", "5610,5611",
		"-wipe-data",
		"-gpu", "swiftshader_indirect",
	}

	if !reflect.DeepEqual(args, expected) {
		t.Fatalf("unexpected args: %#v", args)
	}
}
