// Copyright © 2018 The Knative Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package core

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"

	homedir "github.com/mitchellh/go-homedir"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/crypto/ssh/terminal"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
	"knative.dev/client/pkg/kn/commands"
	"knative.dev/client/pkg/kn/commands/completion"
	"knative.dev/client/pkg/kn/commands/plugin"
	"knative.dev/client/pkg/kn/commands/revision"
	"knative.dev/client/pkg/kn/commands/route"
	"knative.dev/client/pkg/kn/commands/service"
	"knative.dev/client/pkg/kn/commands/source"
	"knative.dev/client/pkg/kn/commands/trigger"
	"knative.dev/client/pkg/kn/commands/version"
	"knative.dev/client/pkg/kn/flags"
)

// NewDefaultKnCommand creates the default `kn` command with a default plugin handler
func NewDefaultKnCommand() *cobra.Command {
	rootCmd := NewKnCommand()

	// Needed since otherwise --plugins-dir and --lookup-plugins
	// will not be accounted for since the plugin is not a Cobra command
	// and will not be parsed
	pluginsDir, lookupPluginsInPath, err := extractKnPluginFlags(os.Args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	pluginHandler := plugin.NewDefaultPluginHandler(plugin.ValidPluginFilenamePrefixes,
		pluginsDir, lookupPluginsInPath)

	return NewDefaultKnCommandWithArgs(rootCmd, pluginHandler,
		os.Args, os.Stdin,
		os.Stdout, os.Stderr)
}

// NewDefaultKnCommandWithArgs creates the `kn` command with arguments
func NewDefaultKnCommandWithArgs(rootCmd *cobra.Command,
	pluginHandler plugin.PluginHandler,
	args []string,
	in io.Reader,
	out,
	errOut io.Writer) *cobra.Command {
	if pluginHandler == nil {
		return rootCmd
	}
	if len(args) > 1 {
		cmdPathPieces := args[1:]
		cmdPathPieces = removeKnPluginFlags(cmdPathPieces) // Plugin does not need these flags

		// only look for suitable extension executables if
		// the specified command does not already exist
		foundCmd, innerArgs, err := rootCmd.Find(cmdPathPieces)
		if err != nil {
			err := plugin.HandlePluginCommand(pluginHandler, cmdPathPieces)
			if err != nil {
				fmt.Fprintf(rootCmd.OutOrStderr(), "Error: unknown command '%s' \nRun 'kn --help' for usage.\n", args[1])
				os.Exit(1)
			}
		} else if foundCmd.HasSubCommands() {
			if _, _, err := rootCmd.Find(innerArgs); err != nil {
				fmt.Fprintf(rootCmd.OutOrStderr(), showSubcommands(foundCmd, cmdPathPieces, innerArgs[0]))
				os.Exit(1)
			}
		}
	}

	return rootCmd
}

// NewKnCommand creates the rootCmd which is the base command when called without any subcommands
func NewKnCommand(params ...commands.KnParams) *cobra.Command {
	var p *commands.KnParams
	if len(params) == 0 {
		p = &commands.KnParams{}
	} else if len(params) == 1 {
		p = &params[0]
	} else {
		panic("Too many params objects to NewKnCommand")
	}
	p.Initialize()

	rootCmd := &cobra.Command{
		Use:   "kn",
		Short: "Knative client",
		Long: `Manage your Knative building blocks:

* Serving: Manage your services and release new software to them.
* Eventing: Manage event subscriptions and channels. Connect up event sources.`,

		// Disable docs header
		DisableAutoGenTag: true,

		// Affects children as well
		SilenceUsage: true,

		// Prevents Cobra from dealing with errors as we deal with them in main.go
		SilenceErrors: true,

		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			initConfigFlags()
			return flags.ReconcileBoolFlags(cmd.Flags())
		},
	}
	if p.Output != nil {
		rootCmd.SetOutput(p.Output)
	}

	// Persistent flags
	rootCmd.PersistentFlags().StringVar(&commands.CfgFile, "config", "", "kn config file (default is $HOME/.kn/config.yaml)")
	rootCmd.PersistentFlags().StringVar(&p.KubeCfgPath, "kubeconfig", "", "kubectl config file (default is $HOME/.kube/config)")
	flags.AddBothBoolFlags(rootCmd.PersistentFlags(), &p.LogHTTP, "log-http", "", false, "log http traffic")

	plugin.AddPluginFlags(rootCmd)
	plugin.BindPluginsFlagToViper(rootCmd)

	// root child commands
	rootCmd.AddCommand(service.NewServiceCommand(p))
	rootCmd.AddCommand(revision.NewRevisionCommand(p))
	rootCmd.AddCommand(plugin.NewPluginCommand(p))
	rootCmd.AddCommand(route.NewRouteCommand(p))
	rootCmd.AddCommand(completion.NewCompletionCommand(p))
	rootCmd.AddCommand(version.NewVersionCommand(p))
	rootCmd.AddCommand(source.NewSourceCommand(p))
	rootCmd.AddCommand(trigger.NewTriggerCommand(p))

	// Initialize default `help` cmd early to prevent unknown command errors
	rootCmd.InitDefaultHelpCmd()

	// Deal with empty and unknown sub command groups
	EmptyAndUnknownSubCommands(rootCmd)

	// Wrap usage.
	w, err := width()
	if err == nil {
		newUsage := strings.ReplaceAll(rootCmd.UsageTemplate(), "FlagUsages ",
			fmt.Sprintf("FlagUsagesWrapped %d ", w))
		rootCmd.SetUsageTemplate(newUsage)
	}

	// For glog parse error.
	flag.CommandLine.Parse([]string{})

	return rootCmd
}

// InitializeConfig initializes the kubeconfig used by all commands
func InitializeConfig() {
	cobra.OnInitialize(initConfig)
}

// EmptyAndUnknownSubCommands adds a RunE to all commands that are groups to
// deal with errors when called with empty or unknown sub command
func EmptyAndUnknownSubCommands(cmd *cobra.Command) {
	for _, childCmd := range cmd.Commands() {
		if childCmd.HasSubCommands() && childCmd.RunE == nil {
			childCmd.RunE = func(aCmd *cobra.Command, args []string) error {
				aCmd.Help()
				if len(args) == 0 {
					return fmt.Errorf("please provide a valid sub-command for \"kn %s\"", aCmd.Name())
				}
				return fmt.Errorf("unknown sub-command \"%s\" for \"kn %s\"", args[0], aCmd.Name())
			}
		}

		// recurse to deal with child commands that are themselves command groups
		EmptyAndUnknownSubCommands(childCmd)
	}
}

// Private

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	if commands.CfgFile != "" {
		// Use config file from the flag.
		viper.SetConfigFile(commands.CfgFile)
	} else {
		// Find home directory.
		home, err := homedir.Dir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}

		// Search config in home directory with name ".kn" (without extension)
		viper.AddConfigPath(path.Join(home, ".kn"))
		viper.SetConfigName("config")
	}

	viper.AutomaticEnv() // read in environment variables that match

	// If a config file is found, read it in.
	err := viper.ReadInConfig()
	if err == nil {
		fmt.Fprintln(os.Stderr, "Using kn config file:", viper.ConfigFileUsed())
	}
}

func initConfigFlags() {
	if viper.IsSet("plugins-dir") {
		commands.Cfg.PluginsDir = viper.GetString("plugins-dir")
	}

	// Always set the Cfg.LookupPlugins from viper value since default is false both ways
	var aBool bool
	aBool = viper.GetBool("lookup-plugins")
	commands.Cfg.LookupPlugins = &aBool
}

func extractKnPluginFlags(args []string) (string, bool, error) {
	pluginsDir := "~/.kn/plugins"
	lookupPluginsInPath := false

	dirFlag := "--plugins-dir"
	pathFlag := "--lookup-plugins"
	var err error

	for _, arg := range args {
		if arg == dirFlag {
			// They forgot the =...
			return "", false, fmt.Errorf("Missing %s flag value", dirFlag)
		} else if strings.HasPrefix(arg, dirFlag+"=") {
			// Starts with --plugins-dir=   so we parse the value
			pluginsDir = arg[len(dirFlag)+1:]
			if pluginsDir == "" {
				// They have a "=" but nothing afer it
				return "", false, fmt.Errorf("Missing %s flag value", dirFlag)
			}
		}

		if arg == pathFlag {
			// just --lookup-plugins   no "="
			lookupPluginsInPath = true
		} else if strings.HasPrefix(arg, pathFlag+"=") {
			// Starts with --lookup-plugins=  so we parse value
			arg = arg[len(pathFlag)+1:]
			if lookupPluginsInPath, err = strconv.ParseBool(arg); err != nil {
				return "", false, fmt.Errorf("Invalid boolean value(%q) for %s flag", arg, dirFlag)
			}
		}
	}
	return pluginsDir, lookupPluginsInPath, nil
}

func removeKnPluginFlags(args []string) []string {
	var remainingArgs []string

	// Remove these two flags from the list of args. Even though some of
	// of these cases should have resulted in an error, if for some reason
	// we got here just remove them anyway.
	for _, arg := range args {
		if arg == "--plugins-dir" ||
			strings.HasPrefix(arg, "--plugins-dir=") ||
			arg == "--lookup-plugins" ||
			strings.HasPrefix(arg, "--lookup-plugins=") {
			continue
		} else {
			remainingArgs = append(remainingArgs, arg)
		}
	}

	return remainingArgs
}

func width() (int, error) {
	width, _, err := terminal.GetSize(int(os.Stdout.Fd()))
	return width, err
}

func getCommands(args []string, innerArg string) string {
	commands := []string{"kn"}
	for _, arg := range args {
		if arg == innerArg {
			return strings.Join(commands, " ")
		}
		commands = append(commands, arg)
	}
	return ""
}

func showSubcommands(cmd *cobra.Command, args []string, innerArg string) string {
	var strs []string
	for _, subcmd := range cmd.Commands() {
		strs = append(strs, subcmd.Name())
	}
	return fmt.Sprintf("Error: unknown subcommand '%s' for '%s'. Available subcommands: %s\nRun 'kn --help' for usage.\n", innerArg, getCommands(args, innerArg), strings.Join(strs, ", "))
}
