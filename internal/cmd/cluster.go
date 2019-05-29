package cmd

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"

	trqhelper "github.com/Donders-Institute/hpc-torque-helper/pkg/client"
	dg "github.com/Donders-Institute/hpc-utility/internal/datagetter"
	"github.com/olekukonko/tablewriter"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var xml bool

const (
	gib float64 = 1024 * 1024 * 1024
)

// variable may be set at the build time to fix the default location for the TorqueHelper server certificate.
var defTorqueHelperCert string
var defMachineListFile string
var vncUser string
var vncMachineListFile string

func init() {

	qstatCmd.Flags().BoolVarP(&xml, "xml", "x", false, "XML output")

	clusterCmd.PersistentFlags().StringVarP(&TorqueServerHost, "server", "s", "torque.dccn.nl", "Torque server hostname")
	clusterCmd.PersistentFlags().IntVarP(&TorqueHelperPort, "port", "p", 60209, "Torque helper service port")
	clusterCmd.PersistentFlags().StringVarP(&TorqueHelperCert, "cert", "c", defTorqueHelperCert, "Torque helper service certificate")

	nodeVncCmd.Flags().StringVarP(&vncUser, "user", "u", "", "username of the VNC owner")
	nodeVncCmd.Flags().StringVarP(&vncMachineListFile, "machine-list", "l", defMachineListFile, "path to the machinelist file")

	nodeCmd.AddCommand(nodeMeminfoCmd, nodeDiskinfoCmd, nodeVncCmd, nodeInfoCmd)
	jobCmd.AddCommand(jobTraceCmd, jobMeminfoCmd)
	clusterCmd.AddCommand(qstatCmd, configCmd, matlabCmd, jobCmd, nodeCmd)

	rootCmd.AddCommand(clusterCmd)
}

var clusterCmd = &cobra.Command{
	Use:   "cluster",
	Short: "Retrieve information about the HPC cluster or a job.",
	Long:  ``,
}

var qstatCmd = &cobra.Command{
	Use:   "qstat",
	Short: "Print job list in the memory of the Torque server.",
	Long:  ``,
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		log.Debug("qstat command is triggerd.")
		c := trqhelper.TorqueHelperSrvClient{
			SrvHost:     TorqueServerHost,
			SrvPort:     TorqueHelperPort,
			SrvCertFile: TorqueHelperCert,
		}
		if err := c.PrintClusterQstat(xml); err != nil {
			log.Errorf("%+v\n", err)
		}
	},
}

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Print Torque and Moab server configurations.",
	Long:  ``,
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {

		if cmd.Flags().Changed("verbose") {
			log.SetLevel(log.DebugLevel)
		}

		c := trqhelper.TorqueHelperSrvClient{
			SrvHost:     TorqueServerHost,
			SrvPort:     TorqueHelperPort,
			SrvCertFile: TorqueHelperCert,
		}
		if err := c.PrintClusterConfig(); err != nil {
			log.Errorf("%+v\n", err)
		}
	},
}

var matlabCmd = &cobra.Command{
	Use:   "matlablic",
	Short: "Print a summary of the Matlab license usage.",
	Long:  ``,
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		stdout, stderr, ec, err := execCmd("lmstat", []string{"-a"})
		if err != nil {
			log.Fatalf("%s: exit code %d\n", err, ec)
		}
		if ec != 0 {
			log.Fatal(stderr.String())
		}

		rePkg := regexp.MustCompile(`^Users of (\S+):  \(Total of (\d+) licenses issued;  Total of (\d+) licenses in use\)$`)
		reUse := regexp.MustCompile(`^\s+(\S+) (\S+).*\((v[0-9]+)\).*, start (.*)$`)
		reRsv := regexp.MustCompile(`^\s+([0-9]+) RESERVATION[s]{0,1} for (HOST_GROUP|GROUP) (\S+)\s+.*$`)

		var lic matlabLicense
		var lics []matlabLicense
		for {
			line, err := stdout.ReadString('\n')
			if err == io.EOF {
				break
			}
			if err != nil {
				log.Fatalln("fail parsing lmstat data")
			}

			line = strings.TrimSuffix(line, "\n")
			if d := rePkg.FindAllStringSubmatch(line, -1); d != nil {

				log.Debugf("find license package: %s\n", line)

				// new license package found, put current lic into lics if the current lic is not nil
				if lic.Package != "" {
					lics = append(lics, lic)
				}

				// create a new matlabLicense with the parsed data
				n := d[0][1]
				t, _ := strconv.Atoi(d[0][2])
				lic = matlabLicense{Package: n, Total: t}

				continue
			}

			if d := reUse.FindAllStringSubmatch(line, -1); d != nil {
				log.Debugf("find package usage: %s\n", line)
				// new license usage found, parse it and add it to the license package's usage attribute.
				usage := matlabLicenseUsageInfo{User: d[0][1], Host: d[0][2], Version: d[0][3], Since: d[0][4]}
				lic.Usages = append(lic.Usages, usage)
				continue
			}

			if d := reRsv.FindAllStringSubmatch(line, -1); d != nil {
				log.Debugf("find package reservation: %s\n", line)
				if nlics, err := strconv.ParseInt(d[0][1], 10, 0); err == nil {
					rsv := matlabLicenseReservationInfo{Group: d[0][3], NumberOfLicense: int(nlics)}
					lic.Reservations = append(lic.Reservations, rsv)
				}
				continue
			}
		}
		// print license usages
		var summaries []string
		for _, lic := range lics {
			if len(lic.Usages) == 0 {
				continue
			}
			table := tablewriter.NewWriter(os.Stdout)
			table.SetHeader([]string{"User", "Host", "Version", "Since"})
			cntLocal := 0
			cntGlobal := 0
			for _, usage := range lic.Usages {
				// TODO: use a better way to filter and present local usage
				if strings.HasSuffix(strings.ToLower(usage.Host), "dccn.nl") || strings.HasPrefix(strings.ToLower(usage.Host), "dccn") {
					table.Append([]string{usage.User, usage.Host, usage.Version, usage.Since})
					cntLocal++
				}
				cntGlobal++
			}
			for _, rsv := range lic.Reservations {
				// NOTE: do not count the reservation as part of the DCCN user usage.
				//       this is compatible with the old cluster-matlab script.
				//
				// if strings.Contains(strings.ToLower(rsv.Group), "dccn") {
				// 	// expand reserved licenses by the number of reservation, is it a good representation??
				// 	for i := 0; i < rsv.NumberOfLicense; i++ {
				// 		table.Append([]string{rsv.Group, "reservation", "", ""})
				// 	}
				// 	cntLocal += rsv.NumberOfLicense
				// }
				cntGlobal += rsv.NumberOfLicense
			}

			if cntLocal > 0 {
				s := fmt.Sprintf("package %s: %d of %d in use (%d by dccn users)", lic.Package, cntGlobal, lic.Total, cntLocal)
				summaries = append(summaries, s)
				fmt.Fprintf(os.Stdout, "\n%s\n", s)
				table.Render()
			}
		}
		// print summary
		fmt.Fprintf(os.Stdout, "Summary:\n")
		for _, s := range summaries {
			fmt.Fprintf(os.Stdout, "%s\n", s)
		}
	},
}

// job related subcommands
var jobCmd = &cobra.Command{
	Use:   "job",
	Short: "Retrieve information about a cluster job.",
	Long:  ``,
}

var jobTraceCmd = &cobra.Command{
	Use:   "trace [JobID]",
	Short: "Print job's trace log available on the Torque server.",
	Long:  ``,
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c := trqhelper.TorqueHelperSrvClient{
			SrvHost:     TorqueServerHost,
			SrvPort:     TorqueHelperPort,
			SrvCertFile: TorqueHelperCert,
		}
		if err := c.PrintClusterTracejob(args[0]); err != nil {
			log.Errorf("fail get job trace info: %+v\n", err)
		}
	},
}

var jobMeminfoCmd = &cobra.Command{
	Use:   "meminfo [JobID]",
	Short: "Print memory usage of a running job.",
	Long:  ``,
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c := trqhelper.TorqueHelperMomClient{
			SrvHost:     TorqueServerHost,
			SrvPort:     TorqueHelperPort,
			SrvCertFile: TorqueHelperCert,
		}
		if err := c.PrintJobMemoryInfo(args[0]); err != nil {
			log.Errorf("fail get job memory utilisation: %+v\n", err)
		}
	},
}

// node related subcommands
type nodeType uint

const (
	access nodeType = iota
	compute
)

var nodeTypeNames = map[nodeType]string{
	access:  "access",
	compute: "compute",
}

var nodeCmd = &cobra.Command{
	Use:   "nodes",
	Short: "Retrieve information about cluster nodes.",
	Long:  ``,
	// ValidArgs: []string{nodeTypeNames[access], nodeTypeNames[compute]},
	// Run: func(cmd *cobra.Command, args []string) {
	// 	// TODO: get nodes overview
	// },
}

var nodeMeminfoCmd = &cobra.Command{
	Use:       "memfree {access|compute}",
	Short:     "Print total and free memory on the cluster nodes.",
	Long:      ``,
	ValidArgs: []string{nodeTypeNames[access], nodeTypeNames[compute]},
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			args = []string{nodeTypeNames[access], nodeTypeNames[compute]}
		}
		for _, n := range args {
			switch n {
			case nodeTypeNames[access]:
				g := dg.GangliaDataGetter{Dataset: dg.MemoryUsageAccessNode}
				g.GetPrint()
			case nodeTypeNames[compute]:
				g := dg.GangliaDataGetter{Dataset: dg.MemoryUsageComputeNode}
				g.GetPrint()
			}
		}
	},
}

var nodeDiskinfoCmd = &cobra.Command{
	Use:       "diskfree {access|compute}",
	Short:     "Print total and free disk space of the cluster nodes.",
	Long:      ``,
	ValidArgs: []string{nodeTypeNames[access], nodeTypeNames[compute]},
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			args = []string{nodeTypeNames[access], nodeTypeNames[compute]}
		}
		for _, n := range args {
			switch n {
			case nodeTypeNames[access]:
				g := dg.GangliaDataGetter{Dataset: dg.DiskUsageAccessNode}
				g.GetPrint()
			case nodeTypeNames[compute]:
				g := dg.GangliaDataGetter{Dataset: dg.DiskUsageComputeNode}
				g.GetPrint()
			}
		}
	},
}

var nodeInfoCmd = &cobra.Command{
	Use:       "info {access|compute}",
	Short:     "Print system load and resource availability of cluster nodes.",
	Long:      ``,
	ValidArgs: []string{nodeTypeNames[access], nodeTypeNames[compute]},
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			args = []string{nodeTypeNames[access], nodeTypeNames[compute]}
		}
		for _, n := range args {
			switch n {
			case nodeTypeNames[access]:
				g := dg.GangliaDataGetter{Dataset: dg.InfoAccessNode}
				g.GetPrint()
			case nodeTypeNames[compute]:
				g := dg.GangliaDataGetter{Dataset: dg.InfoComputeNode}
				g.GetPrint()
			}
		}
	},
}

var nodeVncCmd = &cobra.Command{
	Use:   "vnc [{host1} {host2} ...]",
	Short: "Print list of VNC servers on the cluster or a specific node.",
	Long: `Print list of VNC servers on the cluster or a specific node.

If the {hostname} is specified, only the VNCs on the node will be shown.

When the username is specified by the "-u" option, only the VNCs owned by the user will be shown.`,
	Args: cobra.ArbitraryArgs,
	Run: func(cmd *cobra.Command, args []string) {

		nodes := make(chan string, 4)
		vncservers := make(chan trqhelper.VNCServer)

		// worker group
		wg := new(sync.WaitGroup)
		nworker := 4
		wg.Add(nworker)

		// spin off two gRPC workers as go routines
		for i := 0; i < nworker; i++ {
			go func() {
				c := trqhelper.TorqueHelperAccClient{
					SrvPort:     TorqueHelperPort,
					SrvCertFile: TorqueHelperCert,
				}
				for h := range nodes {
					log.Debugf("work on %s", h)

					c.SrvHost = h
					servers, err := c.GetVNCServers()
					if err != nil {
						log.Errorf("%s: %s", c.SrvHost, err)
					}

					for _, s := range servers {
						if vncUser == "" || s.Owner == vncUser {
							vncservers <- s
						}
					}
				}

				log.Debugln("worker is about to leave")
				wg.Done()
			}()
		}

		// wait for all workers to finish
		go func() {
			wg.Wait()
			close(vncservers)
		}()

		// filling access node hosts
		go func() {
			var mlist []string

			// 1. read machinelist from user provided hosts from commandline arguments
			sort.Strings(args)
			for _, n := range args {
				if !strings.HasSuffix(n, fmt.Sprintf(".%s", NetDomain)) {
					n = fmt.Sprintf("%s.%s", n, NetDomain)
				}
				log.Debugf("add node %s\n", n)
				mlist = append(mlist, n)
			}

			// 2. read machinelist from the machinelist file
			if len(mlist) == 0 {
				// read nodes from user provided machinelist

				if fml, err := os.Open(vncMachineListFile); err == nil {
					defer fml.Close()
					scanner := bufio.NewScanner(fml)
					for scanner.Scan() {
						n := scanner.Text()
						if !strings.HasSuffix(n, fmt.Sprintf(".%s", NetDomain)) {
							n = fmt.Sprintf("%s.%s", n, NetDomain)
						}
						mlist = append(mlist, n)
					}

					if err := scanner.Err(); err != nil {
						log.Warnln(err)
					}
				} else {
					log.Warnln(err)
				}
			}

			// 3. read machinelist from the Gangalia
			if len(mlist) == 0 {
				// TODO: append hostname of all of the access nodes.
				accs, err := dg.GetAccessNodes()
				// sort nodes
				sort.Strings(accs)
				if err != nil {
					log.Errorln(err)
				}

				mlist = append(mlist, accs...)
			}

			// fill mlist to node channel
			for _, n := range mlist {
				nodes <- n
			}
			close(nodes)
		}()

		// reorganise internal data structure for sorting
		var _vncs []trqhelper.VNCServer
		for d := range vncservers {
			_vncs = append(_vncs, d)
		}

		// sort _vncs and make tabluar display on stdout
		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"Username", "VNC session"})

		sort.Slice(_vncs, func(i, j int) bool {

			datai := strings.Split(_vncs[i].ID, ":")
			dataj := strings.Split(_vncs[j].ID, ":")

			hosti := datai[0]
			hostj := dataj[0]

			if hosti != hostj {
				return hosti < hostj
			}

			idi, _ := strconv.ParseUint(datai[1], 10, 32)
			idj, _ := strconv.ParseUint(dataj[1], 10, 32)

			return idi < idj
		})

		for _, vnc := range _vncs {
			table.Append([]string{vnc.Owner, vnc.ID})
		}
		table.Render()
	},
}

// execCmd executes a system call and returns stdout, stderr and exit code of the execution.
func execCmd(cmdName string, cmdArgs []string) (stdout, stderr bytes.Buffer, ec int32, err error) {
	// Execute command and catch the stdout and stderr as byte buffer.
	cmd := exec.Command(cmdName, cmdArgs...)
	cmd.Env = os.Environ()
	cmd.Stderr = &stderr
	cmd.Stdout = &stdout
	ec = 0
	if err = cmd.Run(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			ws := exitError.Sys().(syscall.WaitStatus)
			ec = int32(ws.ExitStatus())
		} else {
			ec = 1
		}
	}
	return
}

// matlabLicense defines data structure of matlab license information and usage parsed from the
// `lmstat -a` command.
type matlabLicense struct {
	Package      string
	Total        int
	Usages       []matlabLicenseUsageInfo
	Reservations []matlabLicenseReservationInfo
}

// matlabLicenseUsageInfo defines data structure of a matlab license that is in use.
type matlabLicenseUsageInfo struct {
	User    string
	Host    string
	Version string
	Since   string
}

// matlabLicenseReservationInfo defines data structure of matlab license reservation.
//
// Note: the reservation is counted as actual usage regardless whether the reservation
//       is actually being used.
type matlabLicenseReservationInfo struct {
	Group           string
	NumberOfLicense int
}
