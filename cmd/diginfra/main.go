package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	stdLog "log"
	"os"
	"runtime/debug"
	"strings"

	"github.com/pkg/errors"
	"github.com/pkg/profile"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/diginfra/diginfra/internal/apiclient"
	"github.com/diginfra/diginfra/internal/clierror"
	"github.com/diginfra/diginfra/internal/config"
	"github.com/diginfra/diginfra/internal/logging"
	"github.com/diginfra/diginfra/internal/ui"
	"github.com/diginfra/diginfra/internal/update"
	"github.com/diginfra/diginfra/internal/version"
)

func init() {
	// set the stdlib default logger to flush to discard, this is done as a number of
	// Terraform libs use the std logger directly, which impacts Diginfra output.
	stdLog.SetOutput(io.Discard)
}

func main() {
	if os.Getenv("DIGINFRA_MEMORY_PROFILE") == "true" {
		defer profile.Start(profile.MemProfile, profile.ProfilePath(".")).Stop()
	} else if os.Getenv("DIGINFRA_CPU_PROFILE") == "true" {
		defer profile.Start(profile.CPUProfile, profile.ProfilePath(".")).Stop()
	}

	Run(nil, nil)
	err := apiclient.GetPricingAPIClient(nil).FlushCache()
	if err != nil {
		logging.Logger.Debug().Err(err).Msg("could not flush pricing API cache to filesystem")
	}
}

// Run starts the Diginfra application with the configured cobra cmds.
// Cmd args and flags are parsed from the cli, but can also be directly injected
// using the modifyCtx and args parameters.
func Run(modifyCtx func(*config.RunContext), args *[]string) {
	ctx, err := config.NewRunContextFromEnv(context.Background())
	if err != nil {
		if err.Error() != "" {
			ui.PrintError(ctx.ErrWriter, err.Error())
		}

		ctx.Exit(1)
	}

	if modifyCtx != nil {
		modifyCtx(ctx)
	}

	var appErr error
	updateMessageChan := make(chan *update.Info)

	defer func() {
		if appErr != nil {
			if v, ok := appErr.(*clierror.PanicError); ok {
				handleUnexpectedErr(ctx, v)
			} else {
				handleCLIError(ctx, appErr)
			}
		}

		unexpectedErr := recover()
		if unexpectedErr != nil {
			panicErr := clierror.NewPanicError(fmt.Errorf("%s", unexpectedErr), debug.Stack())
			handleUnexpectedErr(ctx, panicErr)
		}

		handleUpdateMessage(updateMessageChan)

		if appErr != nil || unexpectedErr != nil {
			ctx.Exit(1)
		}
	}()

	startUpdateCheck(ctx, updateMessageChan)

	rootCmd := newRootCmd(ctx)
	if args != nil {
		rootCmd.SetArgs(*args)
	}

	appErr = rootCmd.Execute()
}

type debugWriter struct {
	f *os.File
}

func (d debugWriter) Write(p []byte) (n int, err error) {
	p = bytes.Trim(p, " \n\t")
	return d.f.Write(append(p, []byte("\n")...))
}

func newRootCmd(ctx *config.RunContext) *cobra.Command {
	rootCmd := &cobra.Command{
		Use:     "diginfra",
		Version: version.Version,
		Short:   "Cloud cost estimates for Terraform",
		Long: fmt.Sprintf(`Diginfra - cloud cost estimates for Terraform

%s
  Quick start: https://diginfra.khulnasoft.com/docs
  Add cost estimates to your pull requests: https://diginfra.khulnasoft.com/cicd`, ui.BoldString("DOCS")),
		Example: `  Show cost diff from Terraform directory:

      diginfra breakdown --path /code --format json --out-file diginfra-base.json
      # Make Terraform code changes
      diginfra diff --path /code --compare-to diginfra-base.json

  Show cost breakdown from Terraform directory:

      diginfra breakdown --path /code --terraform-var-file my.tfvars`,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			ctx.ContextValues.SetValue("command", cmd.Name())
			ctx.CMD = cmd.Name()
			if cmd.Name() == "comment" || (cmd.Parent() != nil && cmd.Parent().Name() == "comment") {
				ctx.SetIsDiginfraComment()
			}
			out, _ := cmd.Flags().GetBool("debug-report")
			if out {
				debugFile := "diginfra-debug-report.json"
				var f *os.File
				var err error

				if _, serr := os.Stat(debugFile); serr != nil {
					f, err = os.OpenFile(debugFile, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0755)
				} else {
					f, err = os.Create(debugFile)
				}

				if err != nil {
					return fmt.Errorf("could not generate debug report file %w", err)
				}
				_, _ = f.WriteString("[\n")

				writer := debugWriter{f: f}
				ctx.ErrWriter = writer
				ctx.Config.SetLogWriter(writer)
			}
			err := loadGlobalFlags(ctx, cmd)
			if err != nil {
				return err
			}

			loadCloudSettings(ctx)

			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// Show the help
			return cmd.Help()
		},
		PersistentPostRunE: func(cmd *cobra.Command, args []string) error {
			out, _ := cmd.Flags().GetBool("debug-report")
			if out {
				if f, ok := ctx.Config.LogWriter().(debugWriter); ok {
					_, _ = f.f.WriteString("{\"msg\":\"program finished\"}\n")

					_, _ = f.f.WriteString("]")
					_ = f.f.Close()
				}
			}

			return nil
		},
	}

	rootCmd.PersistentFlags().Bool("no-color", false, "Turn off colored output")
	rootCmd.PersistentFlags().String("log-level", "", "Log level (trace, debug, info, warn, error, fatal)")
	rootCmd.PersistentFlags().Bool("debug-report", false, "Generate a debug report file which can be sent to Diginfra team")

	rootCmd.AddCommand(authCmd(ctx))
	rootCmd.AddCommand(registerCmd(ctx))
	rootCmd.AddCommand(configureCmd(ctx))
	rootCmd.AddCommand(diffCmd(ctx))
	rootCmd.AddCommand(breakdownCmd(ctx))
	rootCmd.AddCommand(outputCmd(ctx))
	rootCmd.AddCommand(uploadCmd(ctx))
	rootCmd.AddCommand(commentCmd(ctx))
	rootCmd.AddCommand(completionCmd())
	rootCmd.AddCommand(figAutocompleteCmd())
	rootCmd.AddCommand(newGenerateCommand())

	rootCmd.SetUsageTemplate(fmt.Sprintf(`%s{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

%s
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

%s
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}

%s{{range .Commands}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

%s
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

%s
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}

%s{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

Use "{{.CommandPath}} [command] --help" for more information about a command.{{end}}
`,
		ui.BoldString("USAGE"),
		ui.BoldString("ALIAS"),
		ui.BoldString("EXAMPLES"),
		ui.BoldString("AVAILABLE COMMANDS"),
		ui.BoldString("FLAGS"),
		ui.BoldString("GLOBAL FLAGS"),
		ui.BoldString("ADDITIONAL HELP TOPICS"),
	))

	rootCmd.SetVersionTemplate("Diginfra {{.Version}}\n")
	rootCmd.SetOut(ctx.OutWriter)
	rootCmd.SetErr(ctx.ErrWriter)

	return rootCmd
}

func startUpdateCheck(ctx *config.RunContext, c chan *update.Info) {
	go func() {
		updateInfo, err := update.CheckForUpdate(ctx)
		if err != nil {
			logging.Logger.Debug().Err(err).Msg("error checking for Diginfra CLI update")
		}
		c <- updateInfo
		close(c)
	}()
}

func loadCloudSettings(ctx *config.RunContext) {
	if ctx.Config.IsSelfHosted() || (ctx.Config.EnableCloud != nil && !*ctx.Config.EnableCloud) {
		return
	}

	dashboardClient := apiclient.NewDashboardAPIClient(ctx)
	result, err := dashboardClient.QueryCLISettings()
	if err != nil {
		logging.Logger.Debug().Err(err).Msg("Failed to load settings from Diginfra Cloud ")
		// ignore the error so the command can continue without failing
		return
	}
	logging.Logger.Debug().Str("result", fmt.Sprintf("%+v", result)).Msg("Successfully loaded settings from Diginfra Cloud")

	ctx.Config.EnableCloudForOrganization = result.CloudEnabled
	if result.UsageAPIEnabled && ctx.Config.UsageAPIEndpoint == "" {
		ctx.Config.UsageAPIEndpoint = ctx.Config.DashboardAPIEndpoint
		logging.Logger.Debug().Msg("Enabled usage API")
	}
	if result.ActualCostsEnabled && ctx.Config.UsageAPIEndpoint != "" {
		ctx.Config.UsageActualCosts = true
		logging.Logger.Debug().Msg("Enabled actual costs")
	}

	if (result.PoliciesAPIEnabled || result.TagsAPIEnabled) && ctx.Config.PolicyV2APIEndpoint == "" {
		ctx.Config.PolicyV2APIEndpoint = ctx.Config.DashboardAPIEndpoint
		logging.Logger.Debug().Msg("Using default policies V2 endpoint")
	}

	if result.PoliciesAPIEnabled {
		ctx.Config.PoliciesEnabled = true
		logging.Logger.Debug().Msg("Enabled policies V2")
	}

	if result.TagsAPIEnabled {
		ctx.Config.TagPoliciesEnabled = true
		logging.Logger.Debug().Msg("Enabled tag policies")
	}
}

func checkAPIKey(apiKey string, apiEndpoint string, defaultEndpoint string) error {
	if apiEndpoint == defaultEndpoint && apiKey == "" {
		return fmt.Errorf(
			"No DIGINFRA_API_KEY environment variable is set.\nWe run a free Cloud Pricing API, to get an API key run %s",
			ui.PrimaryString("diginfra auth login"),
		)
	}

	return nil
}

var ignoredErrors = []string{
	"Policy check failed",
	"Governance check failed",
}

func handleCLIError(ctx *config.RunContext, cliErr error) {
	if cliErr.Error() != "" {
		ui.PrintError(ctx.ErrWriter, cliErr.Error())
	}

	for _, pattern := range ignoredErrors {
		if strings.Contains(cliErr.Error(), pattern) {
			return
		}
	}

	err := apiclient.ReportCLIError(ctx, cliErr, true)
	if err != nil {
		logging.Logger.Debug().Err(err).Msg("error reporting CLI error")
	}
}

func handleUnexpectedErr(ctx *config.RunContext, err error) {
	ui.PrintUnexpectedErrorStack(err)

	err = apiclient.ReportCLIError(ctx, err, false)
	if err != nil {
		logging.Logger.Debug().Err(err).Msg("error sending unexpected runtime error")
	}
}

func handleUpdateMessage(updateMessageChan chan *update.Info) {
	updateInfo := <-updateMessageChan
	if updateInfo != nil {
		msg := fmt.Sprintf("\n%s %s %s → %s\n%s\n",
			ui.WarningString("Update:"),
			"A new version of Diginfra is available:",
			ui.PrimaryString(version.Version),
			ui.PrimaryString(updateInfo.LatestVersion),
			ui.Indent(updateInfo.Cmd, "  "),
		)
		fmt.Fprint(os.Stderr, msg)
	}
}

func loadGlobalFlags(ctx *config.RunContext, cmd *cobra.Command) error {
	if ctx.IsCIRun() {
		ctx.Config.NoColor = true
	}

	err := ctx.Config.LoadGlobalFlags(cmd)
	if err != nil {
		return err
	}

	ctx.ContextValues.SetValue("dashboardEnabled", ctx.Config.EnableDashboard)
	ctx.ContextValues.SetValue("cloudEnabled", ctx.IsCloudEnabled())
	ctx.ContextValues.SetValue("isDefaultPricingAPIEndpoint", ctx.Config.PricingAPIEndpoint == ctx.Config.DefaultPricingAPIEndpoint)

	flagNames := make([]string, 0)

	cmd.Flags().Visit(func(f *pflag.Flag) {
		flagNames = append(flagNames, f.Name)
	})

	ctx.ContextValues.SetValue("flags", flagNames)

	return nil
}

// saveOutFile saves the output of the command to the file path past in the `--out-file` flag
func saveOutFile(ctx *config.RunContext, cmd *cobra.Command, outFile string, b []byte) error {
	return saveOutFileWithMsg(ctx, cmd, outFile, fmt.Sprintf("Output saved to %s", outFile), b)
}

// saveOutFile saves the output of the command to the file path past in the `--out-file` flag
func saveOutFileWithMsg(ctx *config.RunContext, cmd *cobra.Command, outFile, successMsg string, b []byte) error {
	err := os.WriteFile(outFile, b, 0644) // nolint:gosec
	if err != nil {
		return errors.Wrap(err, "Unable to save output")
	}

	logging.Logger.Info().Msg(successMsg)

	return nil
}