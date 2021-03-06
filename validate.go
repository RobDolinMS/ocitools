package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"reflect"
	"regexp"
	"strings"

	"github.com/Sirupsen/logrus"
	"github.com/codegangsta/cli"
	rspec "github.com/opencontainers/runtime-spec/specs-go"
)

var bundleValidateFlags = []cli.Flag{
	cli.StringFlag{Name: "path", Usage: "path to a bundle"},
}

var (
	defaultRlimits = []string{
		"RLIMIT_CPU",
		"RLIMIT_FSIZE",
		"RLIMIT_DATA",
		"RLIMIT_STACK",
		"RLIMIT_CORE",
		"RLIMIT_RSS",
		"RLIMIT_NPROC",
		"RLIMIT_NOFILE",
		"RLIMIT_MEMLOCK",
		"RLIMIT_AS",
		"RLIMIT_LOCKS",
		"RLIMIT_SIGPENDING",
		"RLIMIT_MSGQUEUE",
		"RLIMIT_NICE",
		"RLIMIT_RTPRIO",
		"RLIMIT_RTTIME",
	}
)

var bundleValidateCommand = cli.Command{
	Name:  "validate",
	Usage: "validate a OCI bundle",
	Flags: bundleValidateFlags,
	Action: func(context *cli.Context) {
		inputPath := context.String("path")
		if inputPath == "" {
			logrus.Fatalf("Bundle path shouldn't be empty")
		}

		if _, err := os.Stat(inputPath); err != nil {
			logrus.Fatal(err)
		}

		sf, err := os.Open(path.Join(inputPath, "config.json"))
		if err != nil {
			logrus.Fatal(err)
		}

		defer sf.Close()

		var spec rspec.Spec
		if err = json.NewDecoder(sf).Decode(&spec); err != nil {
			logrus.Fatal(err)
		} else {
			if spec.Platform.OS != "linux" {
				logrus.Fatalf("Operation system '%s' of the bundle is not supported yet.", spec.Platform.OS)
			}
		}

		rootfsPath := path.Join(inputPath, spec.Root.Path)
		if fi, err := os.Stat(rootfsPath); err != nil {
			logrus.Fatalf("Cannot find the rootfs: %v", rootfsPath)
		} else if !fi.IsDir() {
			logrus.Fatalf("Rootfs: %v is not a directory.", spec.Root.Path)
		}
		bundleValidate(spec, rootfsPath)
		logrus.Infof("Bundle validation succeeded.")
	},
}

func bundleValidate(spec rspec.Spec, rootfs string) {
	checkMandatoryField(spec)
	checkSemVer(spec.Version)
	checkProcess(spec.Process, rootfs)
	checkMounts(spec.Mounts, rootfs)
	checkLinux(spec.Linux, rootfs)
}

func checkSemVer(version string) {
	re, _ := regexp.Compile("^(\\d+)?\\.(\\d+)?\\.(\\d+)?$")
	if ok := re.Match([]byte(version)); !ok {
		logrus.Fatalf("%s is not a valid version format, please read 'SemVer v2.0.0'", version)
	}
}

func checkMounts(mounts []rspec.Mount, rootfs string) {
	for _, mount := range mounts {
		rootfsPath := path.Join(rootfs, mount.Destination)
		if fi, err := os.Stat(rootfsPath); err != nil {
			logrus.Fatalf("Cannot find the mount point: %v", rootfsPath)
		} else if !fi.IsDir() {
			logrus.Fatalf("Mount point: %v is not a directory.", rootfsPath)
		}
	}
}

func checkProcess(process rspec.Process, rootfs string) {
	for index := 0; index < len(process.Capabilities); index++ {
		capability := process.Capabilities[index]
		if !capValid(capability) {
			logrus.Fatalf("%s is not valid, man capabilities(7)", process.Capabilities[index])
		}
	}

	for index := 0; index < len(process.Rlimits); index++ {
		if !rlimitValid(process.Rlimits[index].Type) {
			logrus.Fatalf("Rlimit %v is invalid.", process.Rlimits[index])
		}
	}

	if len(process.ApparmorProfile) > 0 {
		profilePath := path.Join(rootfs, "/etc/apparmor.d", process.ApparmorProfile)
		_, err := os.Stat(profilePath)
		if err != nil {
			logrus.Fatal(err)
		}
	}
}

//Linux only
func checkLinux(spec rspec.Linux, rootfs string) {
	if len(spec.UIDMappings) > 5 {
		logrus.Fatalf("Only 5 UID mappings are allowed (linux kernel restriction).")
	}
	if len(spec.GIDMappings) > 5 {
		logrus.Fatalf("Only 5 GID mappings are allowed (linux kernel restriction).")
	}

	for index := 0; index < len(spec.Namespaces); index++ {
		if !namespaceValid(spec.Namespaces[index]) {
			logrus.Fatalf("Namespace %s is invalid.", spec.Namespaces[index])
		}
	}

	for index := 0; index < len(spec.Devices); index++ {
		if !deviceValid(spec.Devices[index]) {
			logrus.Fatalf("Device %s is invalid.", spec.Devices[index].Path)
		}
	}

	if spec.Seccomp != nil {
		checkSeccomp(*spec.Seccomp)
	}

	switch spec.RootfsPropagation {
	case "":
	case "private":
	case "rprivate":
	case "slave":
	case "rslave":
	case "shared":
	case "rshared":
	default:
		logrus.Fatalf("rootfs-propagation must be empty or one of private|rprivate|slave|rslave|shared|rshared")
	}
}

func checkSeccomp(s rspec.Seccomp) {
	if !seccompActionValid(s.DefaultAction) {
		logrus.Fatalf("Seccomp.DefaultAction is invalid.")
	}
	for index := 0; index < len(s.Syscalls); index++ {
		if !syscallValid(s.Syscalls[index]) {
			logrus.Fatalf("Syscall action is invalid.")
		}
	}
	for index := 0; index < len(s.Architectures); index++ {
		switch s.Architectures[index] {
		case rspec.ArchX86:
		case rspec.ArchX86_64:
		case rspec.ArchX32:
		case rspec.ArchARM:
		case rspec.ArchAARCH64:
		case rspec.ArchMIPS:
		case rspec.ArchMIPS64:
		case rspec.ArchMIPS64N32:
		case rspec.ArchMIPSEL:
		case rspec.ArchMIPSEL64:
		case rspec.ArchMIPSEL64N32:
		default:
			logrus.Fatalf("Seccomp.Architecture [%s] is invalid", s.Architectures[index])
		}
	}
}

func capValid(capability string) bool {
	for _, val := range defaultCaps {
		if val == capability {
			return true
		}
	}
	return false
}

func rlimitValid(rlimit string) bool {
	for _, val := range defaultRlimits {
		if val == rlimit {
			return true
		}
	}
	return false
}

func namespaceValid(ns rspec.Namespace) bool {
	switch ns.Type {
	case rspec.PIDNamespace:
	case rspec.NetworkNamespace:
	case rspec.MountNamespace:
	case rspec.IPCNamespace:
	case rspec.UTSNamespace:
	case rspec.UserNamespace:
	default:
		return false
	}
	return true
}

func deviceValid(d rspec.Device) bool {
	switch d.Type {
	case "b":
	case "c":
	case "u":
		if d.Major <= 0 {
			return false
		}
		if d.Minor <= 0 {
			return false
		}
	case "p":
		if d.Major > 0 || d.Minor > 0 {
			return false
		}
	default:
		return false
	}
	return true
}

func seccompActionValid(secc rspec.Action) bool {
	switch secc {
	case "":
	case rspec.ActKill:
	case rspec.ActTrap:
	case rspec.ActErrno:
	case rspec.ActTrace:
	case rspec.ActAllow:
	default:
		return false
	}
	return true
}

func syscallValid(s rspec.Syscall) bool {
	if !seccompActionValid(s.Action) {
		return false
	}
	for index := 0; index < len(s.Args); index++ {
		arg := s.Args[index]
		switch arg.Op {
		case rspec.OpNotEqual:
		case rspec.OpLessEqual:
		case rspec.OpEqualTo:
		case rspec.OpGreaterEqual:
		case rspec.OpGreaterThan:
		case rspec.OpMaskedEqual:
		default:
			return false
		}
	}
	return true
}

func isStruct(t reflect.Type) bool {
	return t.Kind() == reflect.Struct
}

func isStructPtr(t reflect.Type) bool {
	return t.Kind() == reflect.Ptr && t.Elem().Kind() == reflect.Struct
}

func checkMandatoryUnit(field reflect.Value, tagField reflect.StructField, parent string) ([]string, bool) {
	var msgs []string
	mandatory := !strings.Contains(tagField.Tag.Get("json"), "omitempty")
	switch field.Kind() {
	case reflect.Ptr:
		if mandatory && field.IsNil() == true {
			msgs = append(msgs, fmt.Sprintf("'%s.%s' should not be empty.", parent, tagField.Name))
			return msgs, false
		}
	case reflect.String:
		if mandatory && (field.Len() == 0) {
			msgs = append(msgs, fmt.Sprintf("'%s.%s' should not be empty.", parent, tagField.Name))
			return msgs, false
		}
	case reflect.Slice:
		if mandatory && (field.Len() == 0) {
			msgs = append(msgs, fmt.Sprintf("'%s.%s' should not be empty.", parent, tagField.Name))
			return msgs, false
		}
		valid := true
		for index := 0; index < field.Len(); index++ {
			mValue := field.Index(index)
			if mValue.CanInterface() {
				if ms, ok := checkMandatory(mValue.Interface()); !ok {
					msgs = append(msgs, ms...)
					valid = false
				}
			}
		}
		return msgs, valid
	case reflect.Map:
		if mandatory && ((field.IsNil() == true) || (field.Len() == 0)) {
			msgs = append(msgs, fmt.Sprintf("'%s.%s' should not be empty.", parent, tagField.Name))
			return msgs, false
		}
		valid := true
		keys := field.MapKeys()
		for index := 0; index < len(keys); index++ {
			mValue := field.MapIndex(keys[index])
			if mValue.CanInterface() {
				if ms, ok := checkMandatory(mValue.Interface()); !ok {
					msgs = append(msgs, ms...)
					valid = false
				}
			}
		}
		return msgs, valid
	default:
	}

	return nil, true
}

func checkMandatory(obj interface{}) (msgs []string, valid bool) {
	objT := reflect.TypeOf(obj)
	objV := reflect.ValueOf(obj)
	if isStructPtr(objT) {
		objT = objT.Elem()
		objV = objV.Elem()
	} else if !isStruct(objT) {
		return nil, true
	}

	valid = true
	for i := 0; i < objT.NumField(); i++ {
		t := objT.Field(i).Type
		if isStructPtr(t) && objV.Field(i).IsNil() {
			if !strings.Contains(objT.Field(i).Tag.Get("json"), "omitempty") {
				msgs = append(msgs, fmt.Sprintf("'%s.%s' should not be empty", objT.Name(), objT.Field(i).Name))
				valid = false
			}
		} else if (isStruct(t) || isStructPtr(t)) && objV.Field(i).CanInterface() {
			if ms, ok := checkMandatory(objV.Field(i).Interface()); !ok {
				msgs = append(msgs, ms...)
				valid = false
			}
		} else {
			if ms, ok := checkMandatoryUnit(objV.Field(i), objT.Field(i), objT.Name()); !ok {
				msgs = append(msgs, ms...)
				valid = false
			}
		}

	}
	return msgs, valid
}

func checkMandatoryField(obj interface{}) {
	if msgs, valid := checkMandatory(obj); !valid {
		logrus.Fatalf("Mandatory information missing: %s.", msgs)
	}
}
