package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func configureDynamicCompletion(root *cobra.Command, state *executionState) {
	_ = root.RegisterFlagCompletionFunc("account", completeAccountIDs(state))

	for _, spec := range []struct {
		path []string
		fn   cobra.CompletionFunc
	}{
		{[]string{"account", "show"}, completeAccountIDs(state)},
		{[]string{"account", "rename"}, completeFirstArg(completeAccountIDs(state))},
		{[]string{"account", "default"}, completeFirstArg(completeAccountIDs(state))},
		{[]string{"account", "remove"}, completeFirstArg(completeAccountIDs(state))},
		{[]string{"account", "credential", "show"}, completeFirstArg(completeAccountIDs(state))},
		{[]string{"account", "credential", "replace"}, completeFirstArg(completeAccountIDs(state))},
		{[]string{"account", "credential", "delete"}, completeFirstArg(completeAccountIDs(state))},
		{[]string{"mail", "folders", "show"}, completeFirstArg(completeFolderIDs(state))},
		{[]string{"mail", "folders", "enable"}, completeFirstArg(completeFolderIDs(state))},
		{[]string{"mail", "folders", "disable"}, completeFirstArg(completeFolderIDs(state))},
		{[]string{"mail", "folders", "rename"}, completeFirstArg(completeFolderIDs(state))},
		{[]string{"mail", "folders", "delete"}, completeFirstArg(completeFolderIDs(state))},
		{[]string{"mail", "folders", "subscribe"}, completeFirstArg(completeFolderIDs(state))},
		{[]string{"mail", "folders", "unsubscribe"}, completeFirstArg(completeFolderIDs(state))},
	} {
		if command := findSubcommand(root, spec.path...); command != nil {
			command.ValidArgsFunction = spec.fn
		}
	}

	for _, spec := range []struct {
		path []string
		flag string
	}{
		{[]string{"mail", "sync"}, "folder"},
		{[]string{"mail", "sync", "backfill"}, "folder"},
		{[]string{"mail", "sync", "rebaseline"}, "folder"},
		{[]string{"mail", "draft", "sync"}, "folder"},
		{[]string{"mail", "inbox"}, "folder"},
		{[]string{"mail", "search"}, "folder"},
		{[]string{"mail", "mark-read"}, "folder"},
		{[]string{"mail", "mark-unread"}, "folder"},
		{[]string{"mail", "flag"}, "folder"},
		{[]string{"mail", "unflag"}, "folder"},
		{[]string{"mail", "move"}, "from"},
		{[]string{"mail", "move"}, "to"},
		{[]string{"mail", "copy"}, "from"},
		{[]string{"mail", "copy"}, "to"},
		{[]string{"mail", "archive"}, "from"},
		{[]string{"mail", "unarchive"}, "from"},
		{[]string{"mail", "trash"}, "from"},
		{[]string{"mail", "restore"}, "from"},
		{[]string{"mail", "restore"}, "to"},
		{[]string{"mail", "delete"}, "from"},
	} {
		if command := findSubcommand(root, spec.path...); command != nil && command.Flags().Lookup(spec.flag) != nil {
			_ = command.RegisterFlagCompletionFunc(spec.flag, completeFolderIDs(state))
		}
	}
}

func findSubcommand(root *cobra.Command, path ...string) *cobra.Command {
	current := root
	for _, name := range path {
		var next *cobra.Command
		for _, candidate := range current.Commands() {
			if candidate.Name() == name {
				next = candidate
				break
			}
		}
		if next == nil {
			return nil
		}
		current = next
	}
	return current
}

func completeFirstArg(delegate cobra.CompletionFunc) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return delegate(cmd, args, toComplete)
	}
}

func completeAccountIDs(state *executionState) cobra.CompletionFunc {
	return func(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		values, _, _, err := state.accounts.List(cmd.Context(), state.global.Home, MaximumPageSize)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		result := make([]string, 0, len(values))
		for _, value := range values {
			if !strings.HasPrefix(strings.ToLower(value.ID), strings.ToLower(toComplete)) {
				continue
			}
			description := strings.TrimSpace(value.Name)
			if strings.TrimSpace(value.Email) != "" {
				if description != "" {
					description += " — "
				}
				description += value.Email
			}
			result = append(result, value.ID+"\t"+description)
		}
		return result, cobra.ShellCompDirectiveNoFileComp
	}
}

func completeFolderIDs(state *executionState) cobra.CompletionFunc {
	return func(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		_, values, _, _, err := state.folders.FolderList(cmd.Context(), state.global.Home, state.global.Account, MaximumPageSize, false)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		result := make([]string, 0, len(values))
		for _, value := range values {
			if !strings.HasPrefix(strings.ToLower(value.ID), strings.ToLower(toComplete)) {
				continue
			}
			description := value.Name
			if strings.TrimSpace(string(value.Kind)) != "" {
				description = fmt.Sprintf("%s (%s)", description, value.Kind)
			}
			result = append(result, value.ID+"\t"+description)
		}
		return result, cobra.ShellCompDirectiveNoFileComp
	}
}
