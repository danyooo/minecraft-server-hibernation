package progmgr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/mem"
	"github.com/shirou/gopsutil/process"

	"msh/lib/config"
	"msh/lib/errco"
	"msh/lib/model"
	"msh/lib/servctrl"
)

// buildApi2Req returns Api2Req struct containing data
func buildApi2Req(preTerm bool) *model.Api2Req {
	reqJson := &model.Api2Req{}

	reqJson.Protv = protv

	reqJson.Msh.ID = config.ConfigRuntime.Msh.ID
	reqJson.Msh.Mshv = MshVersion
	reqJson.Msh.Uptime = int(time.Since(msh.startTime).Seconds())
	reqJson.Msh.AllowSuspend = config.ConfigRuntime.Msh.AllowSuspend
	reqJson.Msh.Sgm.Seconds = sgm.stats.seconds
	reqJson.Msh.Sgm.SecondsHibe = sgm.stats.secondsHibe
	reqJson.Msh.Sgm.CpuUsage = sgm.stats.cpuUsage
	reqJson.Msh.Sgm.MemUsage = sgm.stats.memUsage
	reqJson.Msh.Sgm.PlayerSec = sgm.stats.playerSec
	reqJson.Msh.Sgm.PreTerm = preTerm

	reqJson.Machine.Os = runtime.GOOS
	reqJson.Machine.Arch = runtime.GOARCH
	reqJson.Machine.Javav = config.Javav

	// get cpu model and vendor
	if cpuInfo, err := cpu.Info(); err != nil {
		errco.LogMshErr(errco.NewErr(errco.ERROR_GET_CPU_INFO, errco.LVL_D, "buildReq", err.Error())) // non blocking error
		reqJson.Machine.CpuModel = ""
		reqJson.Machine.CpuVendor = ""
	} else {
		if reqJson.Machine.CpuModel = cpuInfo[0].ModelName; reqJson.Machine.CpuModel == "" {
			reqJson.Machine.CpuModel = cpuInfo[0].Model
		}
		reqJson.Machine.CpuVendor = cpuInfo[0].VendorID
	}

	// get cores dedicated to msh
	reqJson.Machine.CoresMsh = runtime.NumCPU()

	// get cores dedicated to system
	if cores, err := cpu.Counts(true); err != nil {
		errco.LogMshErr(errco.NewErr(errco.ERROR_GET_CORES, errco.LVL_D, "buildReq", err.Error())) // non blocking error
		reqJson.Machine.CoresSys = -1
	} else {
		reqJson.Machine.CoresSys = cores
	}

	// get memory dedicated to system
	if memInfo, err := mem.VirtualMemory(); err != nil {
		errco.LogMshErr(errco.NewErr(errco.ERROR_GET_MEMORY, errco.LVL_D, "buildReq", err.Error())) // non blocking error
		reqJson.Machine.Mem = -1
	} else {
		reqJson.Machine.Mem = int(memInfo.Total)
	}

	reqJson.Server.Uptime = servctrl.TermUpTime()
	reqJson.Server.Msv = config.ConfigRuntime.Server.Version
	reqJson.Server.MsProt = config.ConfigRuntime.Server.Protocol

	return reqJson
}

// sendApi2Req sends api2 request
func sendApi2Req(url string, api2req *model.Api2Req) (*http.Response, *errco.Error) {
	// before returning, communicate that request has been sent
	defer func() {
		select {
		case ReqSent <- true:
		default:
		}
	}()

	errco.Logln(errco.LVL_D, "sendApi2Req: sending request...")

	// marshal request struct
	reqByte, err := json.Marshal(api2req)
	if err != nil {
		return nil, errco.NewErr(errco.ERROR_VERSION, errco.LVL_D, "sendApi2Req", err.Error())
	}

	// create http request
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(reqByte))
	if err != nil {
		return nil, errco.NewErr(errco.ERROR_VERSION, errco.LVL_D, "sendApi2Req", err.Error())
	}

	// add header User-Agent, Content-Type
	req.Header.Add("User-Agent", fmt.Sprintf("msh/%s (%s) %s", MshVersion, runtime.GOOS, runtime.GOARCH)) // format: msh/vx.x.x (linux) i386
	req.Header.Set("Content-Type", "application/json")                                                    // necessary for post request

	// execute http request
	errco.Logln(errco.LVL_E, "%smsh --> mshc%s:%v", errco.COLOR_PURPLE, errco.COLOR_RESET, string(reqByte))
	client := &http.Client{Timeout: 4 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return nil, errco.NewErr(errco.ERROR_VERSION, errco.LVL_D, "sendApi2Req", err.Error())
	}

	return res, nil
}

// readApi2Res returns response in api2 struct
func readApi2Res(res *http.Response) (*model.Api2Res, *errco.Error) {
	defer res.Body.Close()

	errco.Logln(errco.LVL_D, "readApi2Res: reading response...")

	// read http response
	resByte, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, errco.NewErr(errco.ERROR_VERSION, errco.LVL_D, "readApi2Res", err.Error())
	}
	errco.Logln(errco.LVL_E, "%smshc --> msh%s:%v", errco.COLOR_PURPLE, errco.COLOR_RESET, resByte)

	// load res data into resJson
	var resJson *model.Api2Res
	err = json.Unmarshal(resByte, &resJson)
	if err != nil {
		return nil, errco.NewErr(errco.ERROR_VERSION, errco.LVL_D, "readApi2Res", err.Error())
	}

	return resJson, nil
}

// getMshTreeStats returns current msh tree cpu/mem usage
func getMshTreeStats() (float64, float64) {
	var mshTreeCpu, mshTreeMem float64 = 0, 0

	if mshProc, err := process.NewProcess(int32(os.Getpid())); err != nil {
		// return current avg usage in case of error
		return sgm.stats.cpuUsage, sgm.stats.memUsage
	} else {
		for _, c := range treeProc(mshProc) {
			if pCpu, err := c.CPUPercent(); err != nil {
				// return current avg usage in case of error
				return sgm.stats.cpuUsage, sgm.stats.memUsage
			} else if pMem, err := c.MemoryPercent(); err != nil {
				// return current avg usage in case of error
				return sgm.stats.cpuUsage, sgm.stats.memUsage
			} else {
				mshTreeCpu += float64(pCpu)
				mshTreeMem += float64(pMem)
			}
		}
	}

	return mshTreeCpu, mshTreeMem
}

// treeProc returns the list of tree pids (with ppid)
func treeProc(proc *process.Process) []*process.Process {
	children, err := proc.Children()
	if err != nil {
		// on linux, if a process does not have children an error is returned
		if err.Error() != "process does not have children" {
			return []*process.Process{proc}
		}
		// return pid -1 to indicate that an error occurred
		return []*process.Process{{Pid: -1}}
	}

	tree := []*process.Process{proc}
	for _, child := range children {
		tree = append(tree, treeProc(child)...)
	}
	return tree
}