//go:build darwin

package sys

/*
#include <libproc.h>
#include <sys/resource.h>
#include <sys/sysctl.h>
#include <mach/mach.h>
#include <ifaddrs.h>
#include <net/if.h>
#include <string.h>
#include <errno.h>

static mach_port_t mc_host(void) {
	static mach_port_t host = MACH_PORT_NULL;
	if (host == MACH_PORT_NULL) host = mach_host_self();
	return host;
}

static int mc_footprint(int pid, unsigned long long *out) {
	struct rusage_info_v4 ri;
	if (proc_pid_rusage(pid, RUSAGE_INFO_V4, (rusage_info_t *)&ri) != 0) return errno;
	*out = ri.ri_phys_footprint;
	return 0;
}

static int mc_cpu_ticks(unsigned long long t[4]) {
	host_cpu_load_info_data_t info;
	mach_msg_type_number_t n = HOST_CPU_LOAD_INFO_COUNT;
	if (host_statistics(mc_host(), HOST_CPU_LOAD_INFO, (host_info_t)&info, &n) != KERN_SUCCESS) return -1;
	for (int i = 0; i < 4; i++) t[i] = info.cpu_ticks[i];
	return 0;
}

static int mc_vm(unsigned long long o[6], unsigned long long *pagesize) {
	vm_statistics64_data_t v;
	mach_msg_type_number_t n = HOST_VM_INFO64_COUNT;
	if (host_statistics64(mc_host(), HOST_VM_INFO64, (host_info64_t)&v, &n) != KERN_SUCCESS) return -1;
	vm_size_t ps = 0;
	host_page_size(mc_host(), &ps);
	*pagesize = ps;
	o[0] = v.free_count;
	o[1] = v.internal_page_count;
	o[2] = v.external_page_count;
	o[3] = v.purgeable_count;
	o[4] = v.wire_count;
	o[5] = v.compressor_page_count;
	return 0;
}

static int mc_memsize(unsigned long long *out) {
	size_t len = sizeof(*out);
	return sysctlbyname("hw.memsize", out, &len, NULL, 0) == 0 ? 0 : errno;
}

static int mc_pressure(int *out) {
	size_t len = sizeof(*out);
	return sysctlbyname("kern.memorystatus_vm_pressure_level", out, &len, NULL, 0) == 0 ? 0 : errno;
}

static int mc_swap(unsigned long long *used) {
	struct xsw_usage sw;
	size_t len = sizeof(sw);
	if (sysctlbyname("vm.swapusage", &sw, &len, NULL, 0) != 0) return errno;
	*used = sw.xsu_used;
	return 0;
}

typedef struct {
	char name[IFNAMSIZ];
	unsigned int in;
	unsigned int out;
} mc_if;

static int mc_ifs(mc_if *buf, int max) {
	struct ifaddrs *ifa, *p;
	int n = 0;
	if (getifaddrs(&ifa) != 0) return -1;
	for (p = ifa; p && n < max; p = p->ifa_next) {
		if (!p->ifa_addr || p->ifa_addr->sa_family != AF_LINK || !p->ifa_data) continue;
		if (p->ifa_flags & IFF_LOOPBACK) continue;
		struct if_data *d = (struct if_data *)p->ifa_data;
		strlcpy(buf[n].name, p->ifa_name, IFNAMSIZ);
		buf[n].in = d->ifi_ibytes;
		buf[n].out = d->ifi_obytes;
		n++;
	}
	freeifaddrs(ifa);
	return n;
}
*/
import "C"

import (
	"context"
	"errors"
	"fmt"

	"github.com/KhomerikiK/soap/internal/model"
)

// Footprint returns a process's physical memory footprint. macOS only allows this for the
// current user's processes unless soap runs as root.
func Footprint(pid int) (uint64, bool) {
	var v C.ulonglong
	if C.mc_footprint(C.int(pid), &v) != 0 {
		return 0, false
	}
	return uint64(v), true
}

// CPUTicks returns busy and total CPU ticks since boot, summed over all cores.
func CPUTicks() (busy, total uint64, err error) {
	var t [4]C.ulonglong
	if C.mc_cpu_ticks(&t[0]) != 0 {
		return 0, 0, errors.New("host_statistics(HOST_CPU_LOAD_INFO) failed")
	}
	user, system, idle, nice := uint64(t[0]), uint64(t[1]), uint64(t[2]), uint64(t[3])
	busy = user + system + nice
	return busy, busy + idle, nil
}

// InterfaceCounters returns byte counters for every network interface except loopback.
func InterfaceCounters() (map[string]model.IfCounters, error) {
	var buf [64]C.mc_if
	n := int(C.mc_ifs(&buf[0], 64))
	if n < 0 {
		return nil, errors.New("getifaddrs failed")
	}
	m := make(map[string]model.IfCounters, n)
	for i := 0; i < n; i++ {
		m[C.GoString(&buf[i].name[0])] = model.IfCounters{In: uint32(buf[i].in), Out: uint32(buf[i].out)}
	}
	return m, nil
}

// MachMemory reads machine-wide memory figures from the kernel.
type MachMemory struct{}

// Stats returns memory use the way Activity Monitor computes it: used is app memory (internal
// pages minus purgeable) plus wired plus compressed; cached is file-backed plus purgeable.
func (MachMemory) Stats(context.Context) (model.MemoryStats, error) {
	var total C.ulonglong
	if r := C.mc_memsize(&total); r != 0 {
		return model.MemoryStats{}, fmt.Errorf("sysctl hw.memsize: errno %d", int(r))
	}
	var v [6]C.ulonglong
	var pageSize C.ulonglong
	if C.mc_vm(&v[0], &pageSize) != 0 {
		return model.MemoryStats{}, errors.New("host_statistics64(HOST_VM_INFO64) failed")
	}
	page := uint64(pageSize)
	internal, external, purgeable := uint64(v[1]), uint64(v[2]), uint64(v[3])
	wired, compressor := uint64(v[4]), uint64(v[5])
	appMem := internal - min(purgeable, internal)
	st := model.MemoryStats{
		Total:      uint64(total),
		Used:       (appMem + wired + compressor) * page,
		Cached:     (external + purgeable) * page,
		Compressed: compressor * page,
	}
	var swapUsed C.ulonglong
	if C.mc_swap(&swapUsed) == 0 {
		st.SwapUsed = uint64(swapUsed)
	}
	var level C.int
	if C.mc_pressure(&level) == 0 {
		st.Pressure = model.PressureFromLevel(int(level))
	}
	return st, nil
}
