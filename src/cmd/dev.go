// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2021-Present The Zarf Authors

// Package cmd contains the CLI commands for Zarf.
package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/defenseunicorns/pkg/helpers/v2"
	"github.com/defenseunicorns/zarf/src/cmd/common"
	"github.com/defenseunicorns/zarf/src/config"
	"github.com/defenseunicorns/zarf/src/config/lang"
	"github.com/defenseunicorns/zarf/src/pkg/message"
	"github.com/defenseunicorns/zarf/src/pkg/packager"
	"github.com/defenseunicorns/zarf/src/pkg/transform"
	"github.com/defenseunicorns/zarf/src/pkg/utils"
	"github.com/defenseunicorns/zarf/src/types"
	"github.com/mholt/archiver/v3"
	"github.com/pterm/pterm"
	"github.com/sergi/go-diff/diffmatchpatch"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var extractPath string

var devCmd = &cobra.Command{
	Use:     "dev",
	Aliases: []string{"prepare", "prep"},
	Short:   lang.CmdDevShort,
}

var devDeployCmd = &cobra.Command{
	Use:   "deploy",
	Args:  cobra.MaximumNArgs(1),
	Short: lang.CmdDevDeployShort,
	Long:  lang.CmdDevDeployLong,
	RunE: func(cmd *cobra.Command, args []string) error {
		pkgConfig.CreateOpts.BaseDir = common.SetBaseDirectory(args)

		v := common.GetViper()
		pkgConfig.CreateOpts.SetVariables = helpers.TransformAndMergeMap(
			v.GetStringMapString(common.VPkgCreateSet), pkgConfig.CreateOpts.SetVariables, strings.ToUpper)

		pkgConfig.PkgOpts.SetVariables = helpers.TransformAndMergeMap(
			v.GetStringMapString(common.VPkgDeploySet), pkgConfig.PkgOpts.SetVariables, strings.ToUpper)

		pkgClient, err := packager.New(&pkgConfig)
		if err != nil {
			return err
		}
		defer pkgClient.ClearTempPaths()

		if err := pkgClient.DevDeploy(cmd.Context()); err != nil {
			return fmt.Errorf("failed to dev deploy: %w", err)
		}
		return nil
	},
}

var devGenerateCmd = &cobra.Command{
	Use:     "generate NAME",
	Aliases: []string{"g"},
	Args:    cobra.ExactArgs(1),
	Short:   lang.CmdDevGenerateShort,
	Example: lang.CmdDevGenerateExample,
	RunE: func(_ *cobra.Command, args []string) error {
		pkgConfig.GenerateOpts.Name = args[0]

		pkgConfig.CreateOpts.BaseDir = "."
		pkgConfig.FindImagesOpts.RepoHelmChartPath = pkgConfig.GenerateOpts.GitPath

		pkgClient, err := packager.New(&pkgConfig)
		if err != nil {
			return err
		}
		defer pkgClient.ClearTempPaths()

		err = pkgClient.Generate()
		if err != nil {
			return err
		}
		return nil
	},
}

var devTransformGitLinksCmd = &cobra.Command{
	Use:     "patch-git HOST FILE",
	Aliases: []string{"p"},
	Short:   lang.CmdDevPatchGitShort,
	Args:    cobra.ExactArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		host, fileName := args[0], args[1]

		// Read the contents of the given file
		content, err := os.ReadFile(fileName)
		if err != nil {
			return fmt.Errorf("unable to read the file %s: %w", fileName, err)
		}

		pkgConfig.InitOpts.GitServer.Address = host

		// Perform git url transformation via regex
		text := string(content)
		processedText := transform.MutateGitURLsInText(message.Warnf, pkgConfig.InitOpts.GitServer.Address, text, pkgConfig.InitOpts.GitServer.PushUsername)

		// Print the differences
		dmp := diffmatchpatch.New()
		diffs := dmp.DiffMain(text, processedText, true)
		diffs = dmp.DiffCleanupSemantic(diffs)
		pterm.Println(dmp.DiffPrettyText(diffs))

		// Ask the user before this destructive action
		confirm := false
		prompt := &survey.Confirm{
			Message: fmt.Sprintf(lang.CmdDevPatchGitOverwritePrompt, fileName),
		}
		if err := survey.AskOne(prompt, &confirm); err != nil {
			return fmt.Errorf("confirm overwrite canceled: %w", err)
		}

		if confirm {
			// Overwrite the file
			err = os.WriteFile(fileName, []byte(processedText), helpers.ReadAllWriteUser)
			if err != nil {
				return fmt.Errorf("unable to write the changes back to the file: %w", err)
			}
		}
		return nil
	},
}

var devSha256SumCmd = &cobra.Command{
	Use:     "sha256sum { FILE | URL }",
	Aliases: []string{"s"},
	Short:   lang.CmdDevSha256sumShort,
	Args:    cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		hashErr := errors.New("unable to compute the SHA256SUM hash")

		fileName := args[0]

		var tmp string
		var data io.ReadCloser
		var err error

		if helpers.IsURL(fileName) {
			message.Warn(lang.CmdDevSha256sumRemoteWarning)

			fileBase, err := helpers.ExtractBasePathFromURL(fileName)
			if err != nil {
				return errors.Join(hashErr, err)
			}

			if fileBase == "" {
				fileBase = "sha-file"
			}

			tmp, err = utils.MakeTempDir(config.CommonOptions.TempDirectory)
			if err != nil {
				return errors.Join(hashErr, err)
			}

			downloadPath := filepath.Join(tmp, fileBase)
			err = utils.DownloadToFile(fileName, downloadPath, "")
			if err != nil {
				return errors.Join(hashErr, err)
			}

			fileName = downloadPath

			defer os.RemoveAll(tmp)
		}

		if extractPath != "" {
			if tmp == "" {
				tmp, err = utils.MakeTempDir(config.CommonOptions.TempDirectory)
				if err != nil {
					return errors.Join(hashErr, err)
				}
				defer os.RemoveAll(tmp)
			}

			extractedFile := filepath.Join(tmp, extractPath)

			err = archiver.Extract(fileName, extractPath, tmp)
			if err != nil {
				return errors.Join(hashErr, err)
			}

			fileName = extractedFile
		}

		data, err = os.Open(fileName)
		if err != nil {
			return errors.Join(hashErr, err)
		}
		defer data.Close()

		hash, err := helpers.GetSHA256Hash(data)
		if err != nil {
			return errors.Join(hashErr, err)
		}
		fmt.Println(hash)
		return nil
	},
}

var devFindImagesCmd = &cobra.Command{
	Use:     "find-images [ PACKAGE ]",
	Aliases: []string{"f"},
	Args:    cobra.MaximumNArgs(1),
	Short:   lang.CmdDevFindImagesShort,
	Long:    lang.CmdDevFindImagesLong,
	RunE: func(cmd *cobra.Command, args []string) error {
		pkgConfig.CreateOpts.BaseDir = common.SetBaseDirectory(args)

		v := common.GetViper()

		pkgConfig.CreateOpts.SetVariables = helpers.TransformAndMergeMap(
			v.GetStringMapString(common.VPkgCreateSet), pkgConfig.CreateOpts.SetVariables, strings.ToUpper)
		pkgConfig.PkgOpts.SetVariables = helpers.TransformAndMergeMap(
			v.GetStringMapString(common.VPkgDeploySet), pkgConfig.PkgOpts.SetVariables, strings.ToUpper)
		pkgClient, err := packager.New(&pkgConfig)
		if err != nil {
			return err
		}
		defer pkgClient.ClearTempPaths()

		if _, err := pkgClient.FindImages(cmd.Context()); err != nil {
			return fmt.Errorf("unable to find images: %w", err)
		}
		return nil
	},
}

var devGenConfigFileCmd = &cobra.Command{
	Use:     "generate-config [ FILENAME ]",
	Aliases: []string{"gc"},
	Args:    cobra.MaximumNArgs(1),
	Short:   lang.CmdDevGenerateConfigShort,
	Long:    lang.CmdDevGenerateConfigLong,
	RunE: func(_ *cobra.Command, args []string) error {
		// If a filename was provided, use that
		fileName := "zarf-config.toml"
		if len(args) > 0 {
			fileName = args[0]
		}

		v := common.GetViper()
		if err := v.SafeWriteConfigAs(fileName); err != nil {
			return fmt.Errorf("unable to write the config file %s, make sure the file doesn't already exist: %w", fileName, err)
		}
		return nil
	},
}

var devLintCmd = &cobra.Command{
	Use:     "lint [ DIRECTORY ]",
	Args:    cobra.MaximumNArgs(1),
	Aliases: []string{"l"},
	Short:   lang.CmdDevLintShort,
	Long:    lang.CmdDevLintLong,
	RunE: func(cmd *cobra.Command, args []string) error {
		pkgConfig.CreateOpts.BaseDir = common.SetBaseDirectory(args)
		v := common.GetViper()
		pkgConfig.CreateOpts.SetVariables = helpers.TransformAndMergeMap(
			v.GetStringMapString(common.VPkgCreateSet), pkgConfig.CreateOpts.SetVariables, strings.ToUpper)

		pkgClient, err := packager.New(&pkgConfig)
		if err != nil {
			return err
		}
		defer pkgClient.ClearTempPaths()

		return pkgClient.Lint(cmd.Context())
	},
}

func init() {
	v := common.GetViper()
	rootCmd.AddCommand(devCmd)

	devCmd.AddCommand(devDeployCmd)
	devCmd.AddCommand(devGenerateCmd)
	devCmd.AddCommand(devTransformGitLinksCmd)
	devCmd.AddCommand(devSha256SumCmd)
	devCmd.AddCommand(devFindImagesCmd)
	devCmd.AddCommand(devGenConfigFileCmd)
	devCmd.AddCommand(devLintCmd)

	bindDevDeployFlags(v)
	bindDevGenerateFlags(v)

	devSha256SumCmd.Flags().StringVarP(&extractPath, "extract-path", "e", "", lang.CmdDevFlagExtractPath)

	devFindImagesCmd.Flags().StringVarP(&pkgConfig.FindImagesOpts.RepoHelmChartPath, "repo-chart-path", "p", "", lang.CmdDevFlagRepoChartPath)
	// use the package create config for this and reset it here to avoid overwriting the config.CreateOptions.SetVariables
	devFindImagesCmd.Flags().StringToStringVar(&pkgConfig.CreateOpts.SetVariables, "set", v.GetStringMapString(common.VPkgCreateSet), lang.CmdDevFlagSet)

	devFindImagesCmd.Flags().MarkDeprecated("set", "this field is replaced by create-set")
	devFindImagesCmd.Flags().MarkHidden("set")
	devFindImagesCmd.Flags().StringVarP(&pkgConfig.CreateOpts.Flavor, "flavor", "f", v.GetString(common.VPkgCreateFlavor), lang.CmdPackageCreateFlagFlavor)
	devFindImagesCmd.Flags().StringToStringVar(&pkgConfig.CreateOpts.SetVariables, "create-set", v.GetStringMapString(common.VPkgCreateSet), lang.CmdDevFlagSet)
	devFindImagesCmd.Flags().StringToStringVar(&pkgConfig.PkgOpts.SetVariables, "deploy-set", v.GetStringMapString(common.VPkgDeploySet), lang.CmdPackageDeployFlagSet)
	// allow for the override of the default helm KubeVersion
	devFindImagesCmd.Flags().StringVar(&pkgConfig.FindImagesOpts.KubeVersionOverride, "kube-version", "", lang.CmdDevFlagKubeVersion)
	// check which manifests are using this particular image
	devFindImagesCmd.Flags().StringVar(&pkgConfig.FindImagesOpts.Why, "why", "", lang.CmdDevFlagFindImagesWhy)
	// skip searching cosign artifacts in find images
	devFindImagesCmd.Flags().BoolVar(&pkgConfig.FindImagesOpts.SkipCosign, "skip-cosign", false, lang.CmdDevFlagFindImagesSkipCosign)

	defaultRegistry := fmt.Sprintf("%s:%d", helpers.IPV4Localhost, types.ZarfInClusterContainerRegistryNodePort)
	devFindImagesCmd.Flags().StringVar(&pkgConfig.FindImagesOpts.RegistryURL, "registry-url", defaultRegistry, lang.CmdDevFlagFindImagesRegistry)

	devLintCmd.Flags().StringToStringVar(&pkgConfig.CreateOpts.SetVariables, "set", v.GetStringMapString(common.VPkgCreateSet), lang.CmdPackageCreateFlagSet)
	devLintCmd.Flags().StringVarP(&pkgConfig.CreateOpts.Flavor, "flavor", "f", v.GetString(common.VPkgCreateFlavor), lang.CmdPackageCreateFlagFlavor)
	devTransformGitLinksCmd.Flags().StringVar(&pkgConfig.InitOpts.GitServer.PushUsername, "git-account", types.ZarfGitPushUser, lang.CmdDevFlagGitAccount)
}

func bindDevDeployFlags(v *viper.Viper) {
	devDeployFlags := devDeployCmd.Flags()

	devDeployFlags.StringToStringVar(&pkgConfig.CreateOpts.SetVariables, "create-set", v.GetStringMapString(common.VPkgCreateSet), lang.CmdPackageCreateFlagSet)
	devDeployFlags.StringToStringVar(&pkgConfig.CreateOpts.RegistryOverrides, "registry-override", v.GetStringMapString(common.VPkgCreateRegistryOverride), lang.CmdPackageCreateFlagRegistryOverride)
	devDeployFlags.StringVarP(&pkgConfig.CreateOpts.Flavor, "flavor", "f", v.GetString(common.VPkgCreateFlavor), lang.CmdPackageCreateFlagFlavor)

	devDeployFlags.StringToStringVar(&pkgConfig.PkgOpts.SetVariables, "deploy-set", v.GetStringMapString(common.VPkgDeploySet), lang.CmdPackageDeployFlagSet)

	// Always require adopt-existing-resources flag (no viper)
	devDeployFlags.BoolVar(&pkgConfig.DeployOpts.AdoptExistingResources, "adopt-existing-resources", false, lang.CmdPackageDeployFlagAdoptExistingResources)
	devDeployFlags.BoolVar(&pkgConfig.DeployOpts.SkipWebhooks, "skip-webhooks", v.GetBool(common.VPkgDeploySkipWebhooks), lang.CmdPackageDeployFlagSkipWebhooks)
	devDeployFlags.DurationVar(&pkgConfig.DeployOpts.Timeout, "timeout", v.GetDuration(common.VPkgDeployTimeout), lang.CmdPackageDeployFlagTimeout)

	devDeployFlags.IntVar(&pkgConfig.PkgOpts.Retries, "retries", v.GetInt(common.VPkgRetries), lang.CmdPackageFlagRetries)
	devDeployFlags.StringVar(&pkgConfig.PkgOpts.OptionalComponents, "components", v.GetString(common.VPkgDeployComponents), lang.CmdPackageDeployFlagComponents)

	devDeployFlags.BoolVar(&pkgConfig.CreateOpts.NoYOLO, "no-yolo", v.GetBool(common.VDevDeployNoYolo), lang.CmdDevDeployFlagNoYolo)
}

func bindDevGenerateFlags(_ *viper.Viper) {
	generateFlags := devGenerateCmd.Flags()

	generateFlags.StringVar(&pkgConfig.GenerateOpts.URL, "url", "", "URL to the source git repository")
	generateFlags.StringVar(&pkgConfig.GenerateOpts.Version, "version", "", "The Version of the chart to use")
	generateFlags.StringVar(&pkgConfig.GenerateOpts.GitPath, "gitPath", "", "Relative path to the chart in the git repository")
	generateFlags.StringVar(&pkgConfig.GenerateOpts.Output, "output-directory", "", "Output directory for the generated zarf.yaml")
	generateFlags.StringVar(&pkgConfig.FindImagesOpts.KubeVersionOverride, "kube-version", "", lang.CmdDevFlagKubeVersion)

	devGenerateCmd.MarkFlagRequired("url")
	devGenerateCmd.MarkFlagRequired("version")
	devGenerateCmd.MarkFlagRequired("output-directory")
}
