# bash completion for icloud                               -*- shell-script -*-

__icloud_debug()
{
    if [[ -n ${BASH_COMP_DEBUG_FILE:-} ]]; then
        echo "$*" >> "${BASH_COMP_DEBUG_FILE}"
    fi
}

# Homebrew on Macs have version 1.3 of bash-completion which doesn't include
# _init_completion. This is a very minimal version of that function.
__icloud_init_completion()
{
    COMPREPLY=()
    _get_comp_words_by_ref "$@" cur prev words cword
}

__icloud_index_of_word()
{
    local w word=$1
    shift
    index=0
    for w in "$@"; do
        [[ $w = "$word" ]] && return
        index=$((index+1))
    done
    index=-1
}

__icloud_contains_word()
{
    local w word=$1; shift
    for w in "$@"; do
        [[ $w = "$word" ]] && return
    done
    return 1
}

__icloud_handle_go_custom_completion()
{
    __icloud_debug "${FUNCNAME[0]}: cur is ${cur}, words[*] is ${words[*]}, #words[@] is ${#words[@]}"

    local shellCompDirectiveError=1
    local shellCompDirectiveNoSpace=2
    local shellCompDirectiveNoFileComp=4
    local shellCompDirectiveFilterFileExt=8
    local shellCompDirectiveFilterDirs=16

    local out requestComp lastParam lastChar comp directive args

    # Prepare the command to request completions for the program.
    # Calling ${words[0]} instead of directly icloud allows handling aliases
    args=("${words[@]:1}")
    # Disable ActiveHelp which is not supported for bash completion v1
    requestComp="ICLOUD_ACTIVE_HELP=0 ${words[0]} __completeNoDesc ${args[*]}"

    lastParam=${words[$((${#words[@]}-1))]}
    lastChar=${lastParam:$((${#lastParam}-1)):1}
    __icloud_debug "${FUNCNAME[0]}: lastParam ${lastParam}, lastChar ${lastChar}"

    if [ -z "${cur}" ] && [ "${lastChar}" != "=" ]; then
        # If the last parameter is complete (there is a space following it)
        # We add an extra empty parameter so we can indicate this to the go method.
        __icloud_debug "${FUNCNAME[0]}: Adding extra empty parameter"
        requestComp="${requestComp} \"\""
    fi

    __icloud_debug "${FUNCNAME[0]}: calling ${requestComp}"
    # Use eval to handle any environment variables and such
    out=$(eval "${requestComp}" 2>/dev/null)

    # Extract the directive integer at the very end of the output following a colon (:)
    directive=${out##*:}
    # Remove the directive
    out=${out%:*}
    if [ "${directive}" = "${out}" ]; then
        # There is not directive specified
        directive=0
    fi
    __icloud_debug "${FUNCNAME[0]}: the completion directive is: ${directive}"
    __icloud_debug "${FUNCNAME[0]}: the completions are: ${out}"

    if [ $((directive & shellCompDirectiveError)) -ne 0 ]; then
        # Error code.  No completion.
        __icloud_debug "${FUNCNAME[0]}: received error from custom completion go code"
        return
    else
        if [ $((directive & shellCompDirectiveNoSpace)) -ne 0 ]; then
            if [[ $(type -t compopt) = "builtin" ]]; then
                __icloud_debug "${FUNCNAME[0]}: activating no space"
                compopt -o nospace
            fi
        fi
        if [ $((directive & shellCompDirectiveNoFileComp)) -ne 0 ]; then
            if [[ $(type -t compopt) = "builtin" ]]; then
                __icloud_debug "${FUNCNAME[0]}: activating no file completion"
                compopt +o default
            fi
        fi
    fi

    if [ $((directive & shellCompDirectiveFilterFileExt)) -ne 0 ]; then
        # File extension filtering
        local fullFilter filter filteringCmd
        # Do not use quotes around the $out variable or else newline
        # characters will be kept.
        for filter in ${out}; do
            fullFilter+="$filter|"
        done

        filteringCmd="_filedir $fullFilter"
        __icloud_debug "File filtering command: $filteringCmd"
        $filteringCmd
    elif [ $((directive & shellCompDirectiveFilterDirs)) -ne 0 ]; then
        # File completion for directories only
        local subdir
        # Use printf to strip any trailing newline
        subdir=$(printf "%s" "${out}")
        if [ -n "$subdir" ]; then
            __icloud_debug "Listing directories in $subdir"
            __icloud_handle_subdirs_in_dir_flag "$subdir"
        else
            __icloud_debug "Listing directories in ."
            _filedir -d
        fi
    else
        while IFS='' read -r comp; do
            COMPREPLY+=("$comp")
        done < <(compgen -W "${out}" -- "$cur")
    fi
}

__icloud_handle_reply()
{
    __icloud_debug "${FUNCNAME[0]}"
    local comp
    case $cur in
        -*)
            if [[ $(type -t compopt) = "builtin" ]]; then
                compopt -o nospace
            fi
            local allflags
            if [ ${#must_have_one_flag[@]} -ne 0 ]; then
                allflags=("${must_have_one_flag[@]}")
            else
                allflags=("${flags[*]} ${two_word_flags[*]}")
            fi
            while IFS='' read -r comp; do
                COMPREPLY+=("$comp")
            done < <(compgen -W "${allflags[*]}" -- "$cur")
            if [[ $(type -t compopt) = "builtin" ]]; then
                [[ "${COMPREPLY[0]}" == *= ]] || compopt +o nospace
            fi

            # complete after --flag=abc
            if [[ $cur == *=* ]]; then
                if [[ $(type -t compopt) = "builtin" ]]; then
                    compopt +o nospace
                fi

                local index flag
                flag="${cur%=*}"
                __icloud_index_of_word "${flag}" "${flags_with_completion[@]}"
                COMPREPLY=()
                if [[ ${index} -ge 0 ]]; then
                    PREFIX=""
                    cur="${cur#*=}"
                    ${flags_completion[${index}]}
                    if [ -n "${ZSH_VERSION:-}" ]; then
                        # zsh completion needs --flag= prefix
                        eval "COMPREPLY=( \"\${COMPREPLY[@]/#/${flag}=}\" )"
                    fi
                fi
            fi

            if [[ -z "${flag_parsing_disabled}" ]]; then
                # If flag parsing is enabled, we have completed the flags and can return.
                # If flag parsing is disabled, we may not know all (or any) of the flags, so we fallthrough
                # to possibly call handle_go_custom_completion.
                return 0;
            fi
            ;;
    esac

    # check if we are handling a flag with special work handling
    local index
    __icloud_index_of_word "${prev}" "${flags_with_completion[@]}"
    if [[ ${index} -ge 0 ]]; then
        ${flags_completion[${index}]}
        return
    fi

    # we are parsing a flag and don't have a special handler, no completion
    if [[ ${cur} != "${words[cword]}" ]]; then
        return
    fi

    local completions
    completions=("${commands[@]}")
    if [[ ${#must_have_one_noun[@]} -ne 0 ]]; then
        completions+=("${must_have_one_noun[@]}")
    elif [[ -n "${has_completion_function}" ]]; then
        # if a go completion function is provided, defer to that function
        __icloud_handle_go_custom_completion
    fi
    if [[ ${#must_have_one_flag[@]} -ne 0 ]]; then
        completions+=("${must_have_one_flag[@]}")
    fi
    while IFS='' read -r comp; do
        COMPREPLY+=("$comp")
    done < <(compgen -W "${completions[*]}" -- "$cur")

    if [[ ${#COMPREPLY[@]} -eq 0 && ${#noun_aliases[@]} -gt 0 && ${#must_have_one_noun[@]} -ne 0 ]]; then
        while IFS='' read -r comp; do
            COMPREPLY+=("$comp")
        done < <(compgen -W "${noun_aliases[*]}" -- "$cur")
    fi

    if [[ ${#COMPREPLY[@]} -eq 0 ]]; then
        if declare -F __icloud_custom_func >/dev/null; then
            # try command name qualified custom func
            __icloud_custom_func
        else
            # otherwise fall back to unqualified for compatibility
            declare -F __custom_func >/dev/null && __custom_func
        fi
    fi

    # available in bash-completion >= 2, not always present on macOS
    if declare -F __ltrim_colon_completions >/dev/null; then
        __ltrim_colon_completions "$cur"
    fi

    # If there is only 1 completion and it is a flag with an = it will be completed
    # but we don't want a space after the =
    if [[ "${#COMPREPLY[@]}" -eq "1" ]] && [[ $(type -t compopt) = "builtin" ]] && [[ "${COMPREPLY[0]}" == --*= ]]; then
       compopt -o nospace
    fi
}

# The arguments should be in the form "ext1|ext2|extn"
__icloud_handle_filename_extension_flag()
{
    local ext="$1"
    _filedir "@(${ext})"
}

__icloud_handle_subdirs_in_dir_flag()
{
    local dir="$1"
    pushd "${dir}" >/dev/null 2>&1 && _filedir -d && popd >/dev/null 2>&1 || return
}

__icloud_handle_flag()
{
    __icloud_debug "${FUNCNAME[0]}: c is $c words[c] is ${words[c]}"

    # if a command required a flag, and we found it, unset must_have_one_flag()
    local flagname=${words[c]}
    local flagvalue=""
    # if the word contained an =
    if [[ ${words[c]} == *"="* ]]; then
        flagvalue=${flagname#*=} # take in as flagvalue after the =
        flagname=${flagname%=*} # strip everything after the =
        flagname="${flagname}=" # but put the = back
    fi
    __icloud_debug "${FUNCNAME[0]}: looking for ${flagname}"
    if __icloud_contains_word "${flagname}" "${must_have_one_flag[@]}"; then
        must_have_one_flag=()
    fi

    # if you set a flag which only applies to this command, don't show subcommands
    if __icloud_contains_word "${flagname}" "${local_nonpersistent_flags[@]}"; then
      commands=()
    fi

    # keep flag value with flagname as flaghash
    # flaghash variable is an associative array which is only supported in bash > 3.
    if [[ -z "${BASH_VERSION:-}" || "${BASH_VERSINFO[0]:-}" -gt 3 ]]; then
        if [ -n "${flagvalue}" ] ; then
            flaghash[${flagname}]=${flagvalue}
        elif [ -n "${words[ $((c+1)) ]}" ] ; then
            flaghash[${flagname}]=${words[ $((c+1)) ]}
        else
            flaghash[${flagname}]="true" # pad "true" for bool flag
        fi
    fi

    # skip the argument to a two word flag
    if [[ ${words[c]} != *"="* ]] && __icloud_contains_word "${words[c]}" "${two_word_flags[@]}"; then
        __icloud_debug "${FUNCNAME[0]}: found a flag ${words[c]}, skip the next argument"
        c=$((c+1))
        # if we are looking for a flags value, don't show commands
        if [[ $c -eq $cword ]]; then
            commands=()
        fi
    fi

    c=$((c+1))

}

__icloud_handle_noun()
{
    __icloud_debug "${FUNCNAME[0]}: c is $c words[c] is ${words[c]}"

    if __icloud_contains_word "${words[c]}" "${must_have_one_noun[@]}"; then
        must_have_one_noun=()
    elif __icloud_contains_word "${words[c]}" "${noun_aliases[@]}"; then
        must_have_one_noun=()
    fi

    nouns+=("${words[c]}")
    c=$((c+1))
}

__icloud_handle_command()
{
    __icloud_debug "${FUNCNAME[0]}: c is $c words[c] is ${words[c]}"

    local next_command
    if [[ -n ${last_command} ]]; then
        next_command="_${last_command}_${words[c]//:/__}"
    else
        if [[ $c -eq 0 ]]; then
            next_command="_icloud_root_command"
        else
            next_command="_${words[c]//:/__}"
        fi
    fi
    c=$((c+1))
    __icloud_debug "${FUNCNAME[0]}: looking for ${next_command}"
    declare -F "$next_command" >/dev/null && $next_command
}

__icloud_handle_word()
{
    if [[ $c -ge $cword ]]; then
        __icloud_handle_reply
        return
    fi
    __icloud_debug "${FUNCNAME[0]}: c is $c words[c] is ${words[c]}"
    if [[ "${words[c]}" == -* ]]; then
        __icloud_handle_flag
    elif __icloud_contains_word "${words[c]}" "${commands[@]}"; then
        __icloud_handle_command
    elif [[ $c -eq 0 ]]; then
        __icloud_handle_command
    elif __icloud_contains_word "${words[c]}" "${command_aliases[@]}"; then
        # aliashash variable is an associative array which is only supported in bash > 3.
        if [[ -z "${BASH_VERSION:-}" || "${BASH_VERSINFO[0]:-}" -gt 3 ]]; then
            words[c]=${aliashash[${words[c]}]}
            __icloud_handle_command
        else
            __icloud_handle_noun
        fi
    else
        __icloud_handle_noun
    fi
    __icloud_handle_word
}

_icloud_account_add()
{
    last_command="icloud_account_add"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--credential-env=")
    two_word_flags+=("--credential-env")
    local_nonpersistent_flags+=("--credential-env")
    local_nonpersistent_flags+=("--credential-env=")
    flags+=("--credential-file=")
    two_word_flags+=("--credential-file")
    local_nonpersistent_flags+=("--credential-file")
    local_nonpersistent_flags+=("--credential-file=")
    flags+=("--default")
    local_nonpersistent_flags+=("--default")
    flags+=("--email=")
    two_word_flags+=("--email")
    local_nonpersistent_flags+=("--email")
    local_nonpersistent_flags+=("--email=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--imap-username=")
    two_word_flags+=("--imap-username")
    local_nonpersistent_flags+=("--imap-username")
    local_nonpersistent_flags+=("--imap-username=")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--name=")
    two_word_flags+=("--name")
    local_nonpersistent_flags+=("--name")
    local_nonpersistent_flags+=("--name=")
    flags+=("--password-stdin")
    local_nonpersistent_flags+=("--password-stdin")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_account_credential_delete()
{
    last_command="icloud_account_credential_delete"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--yes")
    local_nonpersistent_flags+=("--yes")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    has_completion_function=1
    noun_aliases=()
}

_icloud_account_credential_replace()
{
    last_command="icloud_account_credential_replace"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--credential-env=")
    two_word_flags+=("--credential-env")
    local_nonpersistent_flags+=("--credential-env")
    local_nonpersistent_flags+=("--credential-env=")
    flags+=("--credential-file=")
    two_word_flags+=("--credential-file")
    local_nonpersistent_flags+=("--credential-file")
    local_nonpersistent_flags+=("--credential-file=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--password-stdin")
    local_nonpersistent_flags+=("--password-stdin")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    has_completion_function=1
    noun_aliases=()
}

_icloud_account_credential_show()
{
    last_command="icloud_account_credential_show"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    has_completion_function=1
    noun_aliases=()
}

_icloud_account_credential()
{
    last_command="icloud_account_credential"

    command_aliases=()

    commands=()
    commands+=("delete")
    commands+=("replace")
    commands+=("show")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_account_default()
{
    last_command="icloud_account_default"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    has_completion_function=1
    noun_aliases=()
}

_icloud_account_list()
{
    last_command="icloud_account_list"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--cursor=")
    two_word_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--limit=")
    two_word_flags+=("--limit")
    local_nonpersistent_flags+=("--limit")
    local_nonpersistent_flags+=("--limit=")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_account_remove()
{
    last_command="icloud_account_remove"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--yes")
    local_nonpersistent_flags+=("--yes")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    has_completion_function=1
    noun_aliases=()
}

_icloud_account_rename()
{
    last_command="icloud_account_rename"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    has_completion_function=1
    noun_aliases=()
}

_icloud_account_show()
{
    last_command="icloud_account_show"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    has_completion_function=1
    noun_aliases=()
}

_icloud_account_test()
{
    last_command="icloud_account_test"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--all")
    local_nonpersistent_flags+=("--all")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--verbose")
    local_nonpersistent_flags+=("--verbose")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_account()
{
    last_command="icloud_account"

    command_aliases=()

    commands=()
    commands+=("add")
    commands+=("credential")
    commands+=("default")
    commands+=("list")
    commands+=("remove")
    commands+=("rename")
    commands+=("show")
    commands+=("test")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_capabilities()
{
    last_command="icloud_capabilities"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_completion_bash()
{
    last_command="icloud_completion_bash"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_completion_fish()
{
    last_command="icloud_completion_fish"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_completion_powershell()
{
    last_command="icloud_completion_powershell"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_completion_zsh()
{
    last_command="icloud_completion_zsh"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_completion()
{
    last_command="icloud_completion"

    command_aliases=()

    commands=()
    commands+=("bash")
    commands+=("fish")
    commands+=("powershell")
    commands+=("zsh")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_doctor()
{
    last_command="icloud_doctor"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--all")
    local_nonpersistent_flags+=("--all")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--live")
    local_nonpersistent_flags+=("--live")
    flags+=("--verbose")
    local_nonpersistent_flags+=("--verbose")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_help()
{
    last_command="icloud_help"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    has_completion_function=1
    noun_aliases=()
}

_icloud_mail_archive()
{
    last_command="icloud_mail_archive"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry-run")
    local_nonpersistent_flags+=("--dry-run")
    flags+=("--from=")
    two_word_flags+=("--from")
    flags_with_completion+=("--from")
    flags_completion+=("__icloud_handle_go_custom_completion")
    local_nonpersistent_flags+=("--from")
    local_nonpersistent_flags+=("--from=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--idempotency-key=")
    two_word_flags+=("--idempotency-key")
    local_nonpersistent_flags+=("--idempotency-key")
    local_nonpersistent_flags+=("--idempotency-key=")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--to=")
    two_word_flags+=("--to")
    local_nonpersistent_flags+=("--to")
    local_nonpersistent_flags+=("--to=")
    flags+=("--yes")
    local_nonpersistent_flags+=("--yes")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_attachments_list()
{
    last_command="icloud_mail_attachments_list"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--cursor=")
    two_word_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--limit=")
    two_word_flags+=("--limit")
    local_nonpersistent_flags+=("--limit")
    local_nonpersistent_flags+=("--limit=")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_attachments_save()
{
    last_command="icloud_mail_attachments_save"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--binary")
    local_nonpersistent_flags+=("--binary")
    flags+=("--force")
    local_nonpersistent_flags+=("--force")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--output=")
    two_word_flags+=("--output")
    local_nonpersistent_flags+=("--output")
    local_nonpersistent_flags+=("--output=")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_attachments_save-all()
{
    last_command="icloud_mail_attachments_save-all"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dir=")
    two_word_flags+=("--dir")
    local_nonpersistent_flags+=("--dir")
    local_nonpersistent_flags+=("--dir=")
    flags+=("--force")
    local_nonpersistent_flags+=("--force")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_attachments()
{
    last_command="icloud_mail_attachments"

    command_aliases=()

    commands=()
    commands+=("list")
    commands+=("save")
    commands+=("save-all")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_copy()
{
    last_command="icloud_mail_copy"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry-run")
    local_nonpersistent_flags+=("--dry-run")
    flags+=("--from=")
    two_word_flags+=("--from")
    flags_with_completion+=("--from")
    flags_completion+=("__icloud_handle_go_custom_completion")
    local_nonpersistent_flags+=("--from")
    local_nonpersistent_flags+=("--from=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--idempotency-key=")
    two_word_flags+=("--idempotency-key")
    local_nonpersistent_flags+=("--idempotency-key")
    local_nonpersistent_flags+=("--idempotency-key=")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--to=")
    two_word_flags+=("--to")
    flags_with_completion+=("--to")
    flags_completion+=("__icloud_handle_go_custom_completion")
    local_nonpersistent_flags+=("--to")
    local_nonpersistent_flags+=("--to=")
    flags+=("--yes")
    local_nonpersistent_flags+=("--yes")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_delete()
{
    last_command="icloud_mail_delete"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry-run")
    local_nonpersistent_flags+=("--dry-run")
    flags+=("--from=")
    two_word_flags+=("--from")
    flags_with_completion+=("--from")
    flags_completion+=("__icloud_handle_go_custom_completion")
    local_nonpersistent_flags+=("--from")
    local_nonpersistent_flags+=("--from=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--idempotency-key=")
    two_word_flags+=("--idempotency-key")
    local_nonpersistent_flags+=("--idempotency-key")
    local_nonpersistent_flags+=("--idempotency-key=")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--to=")
    two_word_flags+=("--to")
    local_nonpersistent_flags+=("--to")
    local_nonpersistent_flags+=("--to=")
    flags+=("--yes")
    local_nonpersistent_flags+=("--yes")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_draft_create()
{
    last_command="icloud_mail_draft_create"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--attach=")
    two_word_flags+=("--attach")
    local_nonpersistent_flags+=("--attach")
    local_nonpersistent_flags+=("--attach=")
    flags+=("--attach-name=")
    two_word_flags+=("--attach-name")
    local_nonpersistent_flags+=("--attach-name")
    local_nonpersistent_flags+=("--attach-name=")
    flags+=("--attach-type=")
    two_word_flags+=("--attach-type")
    local_nonpersistent_flags+=("--attach-type")
    local_nonpersistent_flags+=("--attach-type=")
    flags+=("--bcc=")
    two_word_flags+=("--bcc")
    local_nonpersistent_flags+=("--bcc")
    local_nonpersistent_flags+=("--bcc=")
    flags+=("--body=")
    two_word_flags+=("--body")
    local_nonpersistent_flags+=("--body")
    local_nonpersistent_flags+=("--body=")
    flags+=("--body-file=")
    two_word_flags+=("--body-file")
    local_nonpersistent_flags+=("--body-file")
    local_nonpersistent_flags+=("--body-file=")
    flags+=("--cc=")
    two_word_flags+=("--cc")
    local_nonpersistent_flags+=("--cc")
    local_nonpersistent_flags+=("--cc=")
    flags+=("--from=")
    two_word_flags+=("--from")
    local_nonpersistent_flags+=("--from")
    local_nonpersistent_flags+=("--from=")
    flags+=("--header=")
    two_word_flags+=("--header")
    local_nonpersistent_flags+=("--header")
    local_nonpersistent_flags+=("--header=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--html-body=")
    two_word_flags+=("--html-body")
    local_nonpersistent_flags+=("--html-body")
    local_nonpersistent_flags+=("--html-body=")
    flags+=("--html-body-file=")
    two_word_flags+=("--html-body-file")
    local_nonpersistent_flags+=("--html-body-file")
    local_nonpersistent_flags+=("--html-body-file=")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--reply-to=")
    two_word_flags+=("--reply-to")
    local_nonpersistent_flags+=("--reply-to")
    local_nonpersistent_flags+=("--reply-to=")
    flags+=("--subject=")
    two_word_flags+=("--subject")
    local_nonpersistent_flags+=("--subject")
    local_nonpersistent_flags+=("--subject=")
    flags+=("--to=")
    two_word_flags+=("--to")
    local_nonpersistent_flags+=("--to")
    local_nonpersistent_flags+=("--to=")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_draft_delete()
{
    last_command="icloud_mail_draft_delete"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry-run")
    local_nonpersistent_flags+=("--dry-run")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--yes")
    local_nonpersistent_flags+=("--yes")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_draft_list()
{
    last_command="icloud_mail_draft_list"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--cursor=")
    two_word_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--limit=")
    two_word_flags+=("--limit")
    local_nonpersistent_flags+=("--limit")
    local_nonpersistent_flags+=("--limit=")
    flags+=("--verbose")
    local_nonpersistent_flags+=("--verbose")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_draft_send()
{
    last_command="icloud_mail_draft_send"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry-run")
    local_nonpersistent_flags+=("--dry-run")
    flags+=("--format=")
    two_word_flags+=("--format")
    local_nonpersistent_flags+=("--format")
    local_nonpersistent_flags+=("--format=")
    flags+=("--from=")
    two_word_flags+=("--from")
    local_nonpersistent_flags+=("--from")
    local_nonpersistent_flags+=("--from=")
    flags+=("--headers")
    local_nonpersistent_flags+=("--headers")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--verbose")
    local_nonpersistent_flags+=("--verbose")
    flags+=("--yes")
    local_nonpersistent_flags+=("--yes")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_draft_show()
{
    last_command="icloud_mail_draft_show"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--body-only")
    local_nonpersistent_flags+=("--body-only")
    flags+=("--format=")
    two_word_flags+=("--format")
    local_nonpersistent_flags+=("--format")
    local_nonpersistent_flags+=("--format=")
    flags+=("--full")
    local_nonpersistent_flags+=("--full")
    flags+=("--headers")
    local_nonpersistent_flags+=("--headers")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--verbose")
    local_nonpersistent_flags+=("--verbose")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_draft_sync()
{
    last_command="icloud_mail_draft_sync"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--folder=")
    two_word_flags+=("--folder")
    flags_with_completion+=("--folder")
    flags_completion+=("__icloud_handle_go_custom_completion")
    local_nonpersistent_flags+=("--folder")
    local_nonpersistent_flags+=("--folder=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--limit=")
    two_word_flags+=("--limit")
    local_nonpersistent_flags+=("--limit")
    local_nonpersistent_flags+=("--limit=")
    flags+=("--verbose")
    local_nonpersistent_flags+=("--verbose")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_draft_update()
{
    last_command="icloud_mail_draft_update"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--attach=")
    two_word_flags+=("--attach")
    local_nonpersistent_flags+=("--attach")
    local_nonpersistent_flags+=("--attach=")
    flags+=("--attach-name=")
    two_word_flags+=("--attach-name")
    local_nonpersistent_flags+=("--attach-name")
    local_nonpersistent_flags+=("--attach-name=")
    flags+=("--attach-type=")
    two_word_flags+=("--attach-type")
    local_nonpersistent_flags+=("--attach-type")
    local_nonpersistent_flags+=("--attach-type=")
    flags+=("--bcc=")
    two_word_flags+=("--bcc")
    local_nonpersistent_flags+=("--bcc")
    local_nonpersistent_flags+=("--bcc=")
    flags+=("--body=")
    two_word_flags+=("--body")
    local_nonpersistent_flags+=("--body")
    local_nonpersistent_flags+=("--body=")
    flags+=("--body-file=")
    two_word_flags+=("--body-file")
    local_nonpersistent_flags+=("--body-file")
    local_nonpersistent_flags+=("--body-file=")
    flags+=("--cc=")
    two_word_flags+=("--cc")
    local_nonpersistent_flags+=("--cc")
    local_nonpersistent_flags+=("--cc=")
    flags+=("--from=")
    two_word_flags+=("--from")
    local_nonpersistent_flags+=("--from")
    local_nonpersistent_flags+=("--from=")
    flags+=("--header=")
    two_word_flags+=("--header")
    local_nonpersistent_flags+=("--header")
    local_nonpersistent_flags+=("--header=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--html-body=")
    two_word_flags+=("--html-body")
    local_nonpersistent_flags+=("--html-body")
    local_nonpersistent_flags+=("--html-body=")
    flags+=("--html-body-file=")
    two_word_flags+=("--html-body-file")
    local_nonpersistent_flags+=("--html-body-file")
    local_nonpersistent_flags+=("--html-body-file=")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--reply-to=")
    two_word_flags+=("--reply-to")
    local_nonpersistent_flags+=("--reply-to")
    local_nonpersistent_flags+=("--reply-to=")
    flags+=("--subject=")
    two_word_flags+=("--subject")
    local_nonpersistent_flags+=("--subject")
    local_nonpersistent_flags+=("--subject=")
    flags+=("--to=")
    two_word_flags+=("--to")
    local_nonpersistent_flags+=("--to")
    local_nonpersistent_flags+=("--to=")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_draft()
{
    last_command="icloud_mail_draft"

    command_aliases=()

    commands=()
    commands+=("create")
    commands+=("delete")
    commands+=("list")
    commands+=("send")
    commands+=("show")
    commands+=("sync")
    commands+=("update")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_flag()
{
    last_command="icloud_mail_flag"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--folder=")
    two_word_flags+=("--folder")
    flags_with_completion+=("--folder")
    flags_completion+=("__icloud_handle_go_custom_completion")
    local_nonpersistent_flags+=("--folder")
    local_nonpersistent_flags+=("--folder=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--input-jsonl=")
    two_word_flags+=("--input-jsonl")
    local_nonpersistent_flags+=("--input-jsonl")
    local_nonpersistent_flags+=("--input-jsonl=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--jsonl")
    local_nonpersistent_flags+=("--jsonl")
    flags+=("--mode=")
    two_word_flags+=("--mode")
    local_nonpersistent_flags+=("--mode")
    local_nonpersistent_flags+=("--mode=")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_folders_create()
{
    last_command="icloud_mail_folders_create"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry-run")
    local_nonpersistent_flags+=("--dry-run")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--yes")
    local_nonpersistent_flags+=("--yes")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_folders_delete()
{
    last_command="icloud_mail_folders_delete"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--allow-non-empty")
    local_nonpersistent_flags+=("--allow-non-empty")
    flags+=("--dry-run")
    local_nonpersistent_flags+=("--dry-run")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--yes")
    local_nonpersistent_flags+=("--yes")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    has_completion_function=1
    noun_aliases=()
}

_icloud_mail_folders_disable()
{
    last_command="icloud_mail_folders_disable"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    has_completion_function=1
    noun_aliases=()
}

_icloud_mail_folders_enable()
{
    last_command="icloud_mail_folders_enable"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    has_completion_function=1
    noun_aliases=()
}

_icloud_mail_folders_list()
{
    last_command="icloud_mail_folders_list"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--cursor=")
    two_word_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--include-missing")
    local_nonpersistent_flags+=("--include-missing")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--limit=")
    two_word_flags+=("--limit")
    local_nonpersistent_flags+=("--limit")
    local_nonpersistent_flags+=("--limit=")
    flags+=("--verbose")
    local_nonpersistent_flags+=("--verbose")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_folders_refresh()
{
    last_command="icloud_mail_folders_refresh"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--cursor=")
    two_word_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--limit=")
    two_word_flags+=("--limit")
    local_nonpersistent_flags+=("--limit")
    local_nonpersistent_flags+=("--limit=")
    flags+=("--verbose")
    local_nonpersistent_flags+=("--verbose")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_folders_rename()
{
    last_command="icloud_mail_folders_rename"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry-run")
    local_nonpersistent_flags+=("--dry-run")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--yes")
    local_nonpersistent_flags+=("--yes")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    has_completion_function=1
    noun_aliases=()
}

_icloud_mail_folders_show()
{
    last_command="icloud_mail_folders_show"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--verbose")
    local_nonpersistent_flags+=("--verbose")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    has_completion_function=1
    noun_aliases=()
}

_icloud_mail_folders_status()
{
    last_command="icloud_mail_folders_status"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--cursor=")
    two_word_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--limit=")
    two_word_flags+=("--limit")
    local_nonpersistent_flags+=("--limit")
    local_nonpersistent_flags+=("--limit=")
    flags+=("--verbose")
    local_nonpersistent_flags+=("--verbose")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_folders_subscribe()
{
    last_command="icloud_mail_folders_subscribe"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry-run")
    local_nonpersistent_flags+=("--dry-run")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--yes")
    local_nonpersistent_flags+=("--yes")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    has_completion_function=1
    noun_aliases=()
}

_icloud_mail_folders_unsubscribe()
{
    last_command="icloud_mail_folders_unsubscribe"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry-run")
    local_nonpersistent_flags+=("--dry-run")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--yes")
    local_nonpersistent_flags+=("--yes")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    has_completion_function=1
    noun_aliases=()
}

_icloud_mail_folders()
{
    last_command="icloud_mail_folders"

    command_aliases=()

    commands=()
    commands+=("create")
    commands+=("delete")
    commands+=("disable")
    commands+=("enable")
    commands+=("list")
    commands+=("refresh")
    commands+=("rename")
    commands+=("show")
    commands+=("status")
    commands+=("subscribe")
    commands+=("unsubscribe")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_forward()
{
    last_command="icloud_mail_forward"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--attach=")
    two_word_flags+=("--attach")
    local_nonpersistent_flags+=("--attach")
    local_nonpersistent_flags+=("--attach=")
    flags+=("--attach-name=")
    two_word_flags+=("--attach-name")
    local_nonpersistent_flags+=("--attach-name")
    local_nonpersistent_flags+=("--attach-name=")
    flags+=("--attach-type=")
    two_word_flags+=("--attach-type")
    local_nonpersistent_flags+=("--attach-type")
    local_nonpersistent_flags+=("--attach-type=")
    flags+=("--bcc=")
    two_word_flags+=("--bcc")
    local_nonpersistent_flags+=("--bcc")
    local_nonpersistent_flags+=("--bcc=")
    flags+=("--body=")
    two_word_flags+=("--body")
    local_nonpersistent_flags+=("--body")
    local_nonpersistent_flags+=("--body=")
    flags+=("--body-file=")
    two_word_flags+=("--body-file")
    local_nonpersistent_flags+=("--body-file")
    local_nonpersistent_flags+=("--body-file=")
    flags+=("--cc=")
    two_word_flags+=("--cc")
    local_nonpersistent_flags+=("--cc")
    local_nonpersistent_flags+=("--cc=")
    flags+=("--dry-run")
    local_nonpersistent_flags+=("--dry-run")
    flags+=("--format=")
    two_word_flags+=("--format")
    local_nonpersistent_flags+=("--format")
    local_nonpersistent_flags+=("--format=")
    flags+=("--from=")
    two_word_flags+=("--from")
    local_nonpersistent_flags+=("--from")
    local_nonpersistent_flags+=("--from=")
    flags+=("--header=")
    two_word_flags+=("--header")
    local_nonpersistent_flags+=("--header")
    local_nonpersistent_flags+=("--header=")
    flags+=("--headers")
    local_nonpersistent_flags+=("--headers")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--html-body=")
    two_word_flags+=("--html-body")
    local_nonpersistent_flags+=("--html-body")
    local_nonpersistent_flags+=("--html-body=")
    flags+=("--html-body-file=")
    two_word_flags+=("--html-body-file")
    local_nonpersistent_flags+=("--html-body-file")
    local_nonpersistent_flags+=("--html-body-file=")
    flags+=("--idempotency-key=")
    two_word_flags+=("--idempotency-key")
    local_nonpersistent_flags+=("--idempotency-key")
    local_nonpersistent_flags+=("--idempotency-key=")
    flags+=("--include-attachment=")
    two_word_flags+=("--include-attachment")
    local_nonpersistent_flags+=("--include-attachment")
    local_nonpersistent_flags+=("--include-attachment=")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--original=")
    two_word_flags+=("--original")
    local_nonpersistent_flags+=("--original")
    local_nonpersistent_flags+=("--original=")
    flags+=("--reply-to=")
    two_word_flags+=("--reply-to")
    local_nonpersistent_flags+=("--reply-to")
    local_nonpersistent_flags+=("--reply-to=")
    flags+=("--retry-of=")
    two_word_flags+=("--retry-of")
    local_nonpersistent_flags+=("--retry-of")
    local_nonpersistent_flags+=("--retry-of=")
    flags+=("--retry-unknown")
    local_nonpersistent_flags+=("--retry-unknown")
    flags+=("--to=")
    two_word_flags+=("--to")
    local_nonpersistent_flags+=("--to")
    local_nonpersistent_flags+=("--to=")
    flags+=("--verbose")
    local_nonpersistent_flags+=("--verbose")
    flags+=("--yes")
    local_nonpersistent_flags+=("--yes")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_identities_add()
{
    last_command="icloud_mail_identities_add"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--address=")
    two_word_flags+=("--address")
    local_nonpersistent_flags+=("--address")
    local_nonpersistent_flags+=("--address=")
    flags+=("--default")
    local_nonpersistent_flags+=("--default")
    flags+=("--display-name=")
    two_word_flags+=("--display-name")
    local_nonpersistent_flags+=("--display-name")
    local_nonpersistent_flags+=("--display-name=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--kind=")
    two_word_flags+=("--kind")
    local_nonpersistent_flags+=("--kind")
    local_nonpersistent_flags+=("--kind=")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_identities_default()
{
    last_command="icloud_mail_identities_default"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_identities_disable()
{
    last_command="icloud_mail_identities_disable"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_identities_enable()
{
    last_command="icloud_mail_identities_enable"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_identities_list()
{
    last_command="icloud_mail_identities_list"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--cursor=")
    two_word_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--limit=")
    two_word_flags+=("--limit")
    local_nonpersistent_flags+=("--limit")
    local_nonpersistent_flags+=("--limit=")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_identities_reclassify()
{
    last_command="icloud_mail_identities_reclassify"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--limit=")
    two_word_flags+=("--limit")
    local_nonpersistent_flags+=("--limit")
    local_nonpersistent_flags+=("--limit=")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_identities_remove()
{
    last_command="icloud_mail_identities_remove"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--yes")
    local_nonpersistent_flags+=("--yes")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_identities_show()
{
    last_command="icloud_mail_identities_show"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_identities_update()
{
    last_command="icloud_mail_identities_update"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--address=")
    two_word_flags+=("--address")
    local_nonpersistent_flags+=("--address")
    local_nonpersistent_flags+=("--address=")
    flags+=("--display-name=")
    two_word_flags+=("--display-name")
    local_nonpersistent_flags+=("--display-name")
    local_nonpersistent_flags+=("--display-name=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--kind=")
    two_word_flags+=("--kind")
    local_nonpersistent_flags+=("--kind")
    local_nonpersistent_flags+=("--kind=")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_identities()
{
    last_command="icloud_mail_identities"

    command_aliases=()

    commands=()
    commands+=("add")
    commands+=("default")
    commands+=("disable")
    commands+=("enable")
    commands+=("list")
    commands+=("reclassify")
    commands+=("remove")
    commands+=("show")
    commands+=("update")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_inbox()
{
    last_command="icloud_mail_inbox"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--after=")
    two_word_flags+=("--after")
    local_nonpersistent_flags+=("--after")
    local_nonpersistent_flags+=("--after=")
    flags+=("--attachment-name=")
    two_word_flags+=("--attachment-name")
    local_nonpersistent_flags+=("--attachment-name")
    local_nonpersistent_flags+=("--attachment-name=")
    flags+=("--attachment-type=")
    two_word_flags+=("--attachment-type")
    local_nonpersistent_flags+=("--attachment-type")
    local_nonpersistent_flags+=("--attachment-type=")
    flags+=("--before=")
    two_word_flags+=("--before")
    local_nonpersistent_flags+=("--before")
    local_nonpersistent_flags+=("--before=")
    flags+=("--cursor=")
    two_word_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor=")
    flags+=("--flagged")
    local_nonpersistent_flags+=("--flagged")
    flags+=("--folder=")
    two_word_flags+=("--folder")
    flags_with_completion+=("--folder")
    flags_completion+=("__icloud_handle_go_custom_completion")
    local_nonpersistent_flags+=("--folder")
    local_nonpersistent_flags+=("--folder=")
    flags+=("--from=")
    two_word_flags+=("--from")
    local_nonpersistent_flags+=("--from")
    local_nonpersistent_flags+=("--from=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--limit=")
    two_word_flags+=("--limit")
    local_nonpersistent_flags+=("--limit")
    local_nonpersistent_flags+=("--limit=")
    flags+=("--participant=")
    two_word_flags+=("--participant")
    local_nonpersistent_flags+=("--participant")
    local_nonpersistent_flags+=("--participant=")
    flags+=("--query=")
    two_word_flags+=("--query")
    local_nonpersistent_flags+=("--query")
    local_nonpersistent_flags+=("--query=")
    flags+=("--read")
    local_nonpersistent_flags+=("--read")
    flags+=("--rfc-id=")
    two_word_flags+=("--rfc-id")
    local_nonpersistent_flags+=("--rfc-id")
    local_nonpersistent_flags+=("--rfc-id=")
    flags+=("--subject=")
    two_word_flags+=("--subject")
    local_nonpersistent_flags+=("--subject")
    local_nonpersistent_flags+=("--subject=")
    flags+=("--unflagged")
    local_nonpersistent_flags+=("--unflagged")
    flags+=("--unread")
    local_nonpersistent_flags+=("--unread")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_latest()
{
    last_command="icloud_mail_latest"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--after=")
    two_word_flags+=("--after")
    local_nonpersistent_flags+=("--after")
    local_nonpersistent_flags+=("--after=")
    flags+=("--attachment-name=")
    two_word_flags+=("--attachment-name")
    local_nonpersistent_flags+=("--attachment-name")
    local_nonpersistent_flags+=("--attachment-name=")
    flags+=("--attachment-type=")
    two_word_flags+=("--attachment-type")
    local_nonpersistent_flags+=("--attachment-type")
    local_nonpersistent_flags+=("--attachment-type=")
    flags+=("--before=")
    two_word_flags+=("--before")
    local_nonpersistent_flags+=("--before")
    local_nonpersistent_flags+=("--before=")
    flags+=("--cursor=")
    two_word_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor=")
    flags+=("--flagged")
    local_nonpersistent_flags+=("--flagged")
    flags+=("--folder=")
    two_word_flags+=("--folder")
    local_nonpersistent_flags+=("--folder")
    local_nonpersistent_flags+=("--folder=")
    flags+=("--from=")
    two_word_flags+=("--from")
    local_nonpersistent_flags+=("--from")
    local_nonpersistent_flags+=("--from=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--limit=")
    two_word_flags+=("--limit")
    local_nonpersistent_flags+=("--limit")
    local_nonpersistent_flags+=("--limit=")
    flags+=("--participant=")
    two_word_flags+=("--participant")
    local_nonpersistent_flags+=("--participant")
    local_nonpersistent_flags+=("--participant=")
    flags+=("--query=")
    two_word_flags+=("--query")
    local_nonpersistent_flags+=("--query")
    local_nonpersistent_flags+=("--query=")
    flags+=("--read")
    local_nonpersistent_flags+=("--read")
    flags+=("--rfc-id=")
    two_word_flags+=("--rfc-id")
    local_nonpersistent_flags+=("--rfc-id")
    local_nonpersistent_flags+=("--rfc-id=")
    flags+=("--subject=")
    two_word_flags+=("--subject")
    local_nonpersistent_flags+=("--subject")
    local_nonpersistent_flags+=("--subject=")
    flags+=("--unflagged")
    local_nonpersistent_flags+=("--unflagged")
    flags+=("--unread")
    local_nonpersistent_flags+=("--unread")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_mark-read()
{
    last_command="icloud_mail_mark-read"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--folder=")
    two_word_flags+=("--folder")
    flags_with_completion+=("--folder")
    flags_completion+=("__icloud_handle_go_custom_completion")
    local_nonpersistent_flags+=("--folder")
    local_nonpersistent_flags+=("--folder=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--input-jsonl=")
    two_word_flags+=("--input-jsonl")
    local_nonpersistent_flags+=("--input-jsonl")
    local_nonpersistent_flags+=("--input-jsonl=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--jsonl")
    local_nonpersistent_flags+=("--jsonl")
    flags+=("--mode=")
    two_word_flags+=("--mode")
    local_nonpersistent_flags+=("--mode")
    local_nonpersistent_flags+=("--mode=")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_mark-unread()
{
    last_command="icloud_mail_mark-unread"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--folder=")
    two_word_flags+=("--folder")
    flags_with_completion+=("--folder")
    flags_completion+=("__icloud_handle_go_custom_completion")
    local_nonpersistent_flags+=("--folder")
    local_nonpersistent_flags+=("--folder=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--input-jsonl=")
    two_word_flags+=("--input-jsonl")
    local_nonpersistent_flags+=("--input-jsonl")
    local_nonpersistent_flags+=("--input-jsonl=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--jsonl")
    local_nonpersistent_flags+=("--jsonl")
    flags+=("--mode=")
    two_word_flags+=("--mode")
    local_nonpersistent_flags+=("--mode")
    local_nonpersistent_flags+=("--mode=")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_message_export()
{
    last_command="icloud_mail_message_export"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--force")
    local_nonpersistent_flags+=("--force")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--output=")
    two_word_flags+=("--output")
    local_nonpersistent_flags+=("--output")
    local_nonpersistent_flags+=("--output=")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_message_raw()
{
    last_command="icloud_mail_message_raw"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_message_show()
{
    last_command="icloud_mail_message_show"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--body-only")
    local_nonpersistent_flags+=("--body-only")
    flags+=("--cache")
    local_nonpersistent_flags+=("--cache")
    flags+=("--format=")
    two_word_flags+=("--format")
    local_nonpersistent_flags+=("--format")
    local_nonpersistent_flags+=("--format=")
    flags+=("--full")
    local_nonpersistent_flags+=("--full")
    flags+=("--headers")
    local_nonpersistent_flags+=("--headers")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--metadata-only")
    local_nonpersistent_flags+=("--metadata-only")
    flags+=("--remote")
    local_nonpersistent_flags+=("--remote")
    flags+=("--verbose")
    local_nonpersistent_flags+=("--verbose")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_message()
{
    last_command="icloud_mail_message"

    command_aliases=()

    commands=()
    commands+=("export")
    commands+=("raw")
    commands+=("show")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_messages_list()
{
    last_command="icloud_mail_messages_list"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--after=")
    two_word_flags+=("--after")
    local_nonpersistent_flags+=("--after")
    local_nonpersistent_flags+=("--after=")
    flags+=("--attachment-name=")
    two_word_flags+=("--attachment-name")
    local_nonpersistent_flags+=("--attachment-name")
    local_nonpersistent_flags+=("--attachment-name=")
    flags+=("--attachment-type=")
    two_word_flags+=("--attachment-type")
    local_nonpersistent_flags+=("--attachment-type")
    local_nonpersistent_flags+=("--attachment-type=")
    flags+=("--before=")
    two_word_flags+=("--before")
    local_nonpersistent_flags+=("--before")
    local_nonpersistent_flags+=("--before=")
    flags+=("--cursor=")
    two_word_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor=")
    flags+=("--flagged")
    local_nonpersistent_flags+=("--flagged")
    flags+=("--folder=")
    two_word_flags+=("--folder")
    local_nonpersistent_flags+=("--folder")
    local_nonpersistent_flags+=("--folder=")
    flags+=("--from=")
    two_word_flags+=("--from")
    local_nonpersistent_flags+=("--from")
    local_nonpersistent_flags+=("--from=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--limit=")
    two_word_flags+=("--limit")
    local_nonpersistent_flags+=("--limit")
    local_nonpersistent_flags+=("--limit=")
    flags+=("--participant=")
    two_word_flags+=("--participant")
    local_nonpersistent_flags+=("--participant")
    local_nonpersistent_flags+=("--participant=")
    flags+=("--query=")
    two_word_flags+=("--query")
    local_nonpersistent_flags+=("--query")
    local_nonpersistent_flags+=("--query=")
    flags+=("--read")
    local_nonpersistent_flags+=("--read")
    flags+=("--rfc-id=")
    two_word_flags+=("--rfc-id")
    local_nonpersistent_flags+=("--rfc-id")
    local_nonpersistent_flags+=("--rfc-id=")
    flags+=("--subject=")
    two_word_flags+=("--subject")
    local_nonpersistent_flags+=("--subject")
    local_nonpersistent_flags+=("--subject=")
    flags+=("--unflagged")
    local_nonpersistent_flags+=("--unflagged")
    flags+=("--unread")
    local_nonpersistent_flags+=("--unread")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_messages()
{
    last_command="icloud_mail_messages"

    command_aliases=()

    commands=()
    commands+=("list")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_move()
{
    last_command="icloud_mail_move"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry-run")
    local_nonpersistent_flags+=("--dry-run")
    flags+=("--from=")
    two_word_flags+=("--from")
    flags_with_completion+=("--from")
    flags_completion+=("__icloud_handle_go_custom_completion")
    local_nonpersistent_flags+=("--from")
    local_nonpersistent_flags+=("--from=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--idempotency-key=")
    two_word_flags+=("--idempotency-key")
    local_nonpersistent_flags+=("--idempotency-key")
    local_nonpersistent_flags+=("--idempotency-key=")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--to=")
    two_word_flags+=("--to")
    flags_with_completion+=("--to")
    flags_completion+=("__icloud_handle_go_custom_completion")
    local_nonpersistent_flags+=("--to")
    local_nonpersistent_flags+=("--to=")
    flags+=("--yes")
    local_nonpersistent_flags+=("--yes")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_operations_mutations_list()
{
    last_command="icloud_mail_operations_mutations_list"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--cursor=")
    two_word_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--limit=")
    two_word_flags+=("--limit")
    local_nonpersistent_flags+=("--limit")
    local_nonpersistent_flags+=("--limit=")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_operations_mutations_show()
{
    last_command="icloud_mail_operations_mutations_show"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_operations_mutations()
{
    last_command="icloud_mail_operations_mutations"

    command_aliases=()

    commands=()
    commands+=("list")
    commands+=("show")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_operations_outbound_list()
{
    last_command="icloud_mail_operations_outbound_list"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--cursor=")
    two_word_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--limit=")
    two_word_flags+=("--limit")
    local_nonpersistent_flags+=("--limit")
    local_nonpersistent_flags+=("--limit=")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_operations_outbound_show()
{
    last_command="icloud_mail_operations_outbound_show"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_operations_outbound()
{
    last_command="icloud_mail_operations_outbound"

    command_aliases=()

    commands=()
    commands+=("list")
    commands+=("show")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_operations()
{
    last_command="icloud_mail_operations"

    command_aliases=()

    commands=()
    commands+=("mutations")
    commands+=("outbound")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_reply()
{
    last_command="icloud_mail_reply"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--attach=")
    two_word_flags+=("--attach")
    local_nonpersistent_flags+=("--attach")
    local_nonpersistent_flags+=("--attach=")
    flags+=("--attach-name=")
    two_word_flags+=("--attach-name")
    local_nonpersistent_flags+=("--attach-name")
    local_nonpersistent_flags+=("--attach-name=")
    flags+=("--attach-type=")
    two_word_flags+=("--attach-type")
    local_nonpersistent_flags+=("--attach-type")
    local_nonpersistent_flags+=("--attach-type=")
    flags+=("--bcc=")
    two_word_flags+=("--bcc")
    local_nonpersistent_flags+=("--bcc")
    local_nonpersistent_flags+=("--bcc=")
    flags+=("--body=")
    two_word_flags+=("--body")
    local_nonpersistent_flags+=("--body")
    local_nonpersistent_flags+=("--body=")
    flags+=("--body-file=")
    two_word_flags+=("--body-file")
    local_nonpersistent_flags+=("--body-file")
    local_nonpersistent_flags+=("--body-file=")
    flags+=("--cc=")
    two_word_flags+=("--cc")
    local_nonpersistent_flags+=("--cc")
    local_nonpersistent_flags+=("--cc=")
    flags+=("--dry-run")
    local_nonpersistent_flags+=("--dry-run")
    flags+=("--format=")
    two_word_flags+=("--format")
    local_nonpersistent_flags+=("--format")
    local_nonpersistent_flags+=("--format=")
    flags+=("--from=")
    two_word_flags+=("--from")
    local_nonpersistent_flags+=("--from")
    local_nonpersistent_flags+=("--from=")
    flags+=("--header=")
    two_word_flags+=("--header")
    local_nonpersistent_flags+=("--header")
    local_nonpersistent_flags+=("--header=")
    flags+=("--headers")
    local_nonpersistent_flags+=("--headers")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--html-body=")
    two_word_flags+=("--html-body")
    local_nonpersistent_flags+=("--html-body")
    local_nonpersistent_flags+=("--html-body=")
    flags+=("--html-body-file=")
    two_word_flags+=("--html-body-file")
    local_nonpersistent_flags+=("--html-body-file")
    local_nonpersistent_flags+=("--html-body-file=")
    flags+=("--idempotency-key=")
    two_word_flags+=("--idempotency-key")
    local_nonpersistent_flags+=("--idempotency-key")
    local_nonpersistent_flags+=("--idempotency-key=")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--replace-cc")
    local_nonpersistent_flags+=("--replace-cc")
    flags+=("--replace-to")
    local_nonpersistent_flags+=("--replace-to")
    flags+=("--retry-of=")
    two_word_flags+=("--retry-of")
    local_nonpersistent_flags+=("--retry-of")
    local_nonpersistent_flags+=("--retry-of=")
    flags+=("--retry-unknown")
    local_nonpersistent_flags+=("--retry-unknown")
    flags+=("--to=")
    two_word_flags+=("--to")
    local_nonpersistent_flags+=("--to")
    local_nonpersistent_flags+=("--to=")
    flags+=("--verbose")
    local_nonpersistent_flags+=("--verbose")
    flags+=("--yes")
    local_nonpersistent_flags+=("--yes")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_reply-all()
{
    last_command="icloud_mail_reply-all"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--attach=")
    two_word_flags+=("--attach")
    local_nonpersistent_flags+=("--attach")
    local_nonpersistent_flags+=("--attach=")
    flags+=("--attach-name=")
    two_word_flags+=("--attach-name")
    local_nonpersistent_flags+=("--attach-name")
    local_nonpersistent_flags+=("--attach-name=")
    flags+=("--attach-type=")
    two_word_flags+=("--attach-type")
    local_nonpersistent_flags+=("--attach-type")
    local_nonpersistent_flags+=("--attach-type=")
    flags+=("--bcc=")
    two_word_flags+=("--bcc")
    local_nonpersistent_flags+=("--bcc")
    local_nonpersistent_flags+=("--bcc=")
    flags+=("--body=")
    two_word_flags+=("--body")
    local_nonpersistent_flags+=("--body")
    local_nonpersistent_flags+=("--body=")
    flags+=("--body-file=")
    two_word_flags+=("--body-file")
    local_nonpersistent_flags+=("--body-file")
    local_nonpersistent_flags+=("--body-file=")
    flags+=("--cc=")
    two_word_flags+=("--cc")
    local_nonpersistent_flags+=("--cc")
    local_nonpersistent_flags+=("--cc=")
    flags+=("--dry-run")
    local_nonpersistent_flags+=("--dry-run")
    flags+=("--format=")
    two_word_flags+=("--format")
    local_nonpersistent_flags+=("--format")
    local_nonpersistent_flags+=("--format=")
    flags+=("--from=")
    two_word_flags+=("--from")
    local_nonpersistent_flags+=("--from")
    local_nonpersistent_flags+=("--from=")
    flags+=("--header=")
    two_word_flags+=("--header")
    local_nonpersistent_flags+=("--header")
    local_nonpersistent_flags+=("--header=")
    flags+=("--headers")
    local_nonpersistent_flags+=("--headers")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--html-body=")
    two_word_flags+=("--html-body")
    local_nonpersistent_flags+=("--html-body")
    local_nonpersistent_flags+=("--html-body=")
    flags+=("--html-body-file=")
    two_word_flags+=("--html-body-file")
    local_nonpersistent_flags+=("--html-body-file")
    local_nonpersistent_flags+=("--html-body-file=")
    flags+=("--idempotency-key=")
    two_word_flags+=("--idempotency-key")
    local_nonpersistent_flags+=("--idempotency-key")
    local_nonpersistent_flags+=("--idempotency-key=")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--replace-cc")
    local_nonpersistent_flags+=("--replace-cc")
    flags+=("--replace-to")
    local_nonpersistent_flags+=("--replace-to")
    flags+=("--retry-of=")
    two_word_flags+=("--retry-of")
    local_nonpersistent_flags+=("--retry-of")
    local_nonpersistent_flags+=("--retry-of=")
    flags+=("--retry-unknown")
    local_nonpersistent_flags+=("--retry-unknown")
    flags+=("--to=")
    two_word_flags+=("--to")
    local_nonpersistent_flags+=("--to")
    local_nonpersistent_flags+=("--to=")
    flags+=("--verbose")
    local_nonpersistent_flags+=("--verbose")
    flags+=("--yes")
    local_nonpersistent_flags+=("--yes")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_restore()
{
    last_command="icloud_mail_restore"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry-run")
    local_nonpersistent_flags+=("--dry-run")
    flags+=("--from=")
    two_word_flags+=("--from")
    flags_with_completion+=("--from")
    flags_completion+=("__icloud_handle_go_custom_completion")
    local_nonpersistent_flags+=("--from")
    local_nonpersistent_flags+=("--from=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--idempotency-key=")
    two_word_flags+=("--idempotency-key")
    local_nonpersistent_flags+=("--idempotency-key")
    local_nonpersistent_flags+=("--idempotency-key=")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--to=")
    two_word_flags+=("--to")
    flags_with_completion+=("--to")
    flags_completion+=("__icloud_handle_go_custom_completion")
    local_nonpersistent_flags+=("--to")
    local_nonpersistent_flags+=("--to=")
    flags+=("--yes")
    local_nonpersistent_flags+=("--yes")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_search_repair()
{
    last_command="icloud_mail_search_repair"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--limit=")
    two_word_flags+=("--limit")
    local_nonpersistent_flags+=("--limit")
    local_nonpersistent_flags+=("--limit=")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_search()
{
    last_command="icloud_mail_search"

    command_aliases=()

    commands=()
    commands+=("repair")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--after=")
    two_word_flags+=("--after")
    local_nonpersistent_flags+=("--after")
    local_nonpersistent_flags+=("--after=")
    flags+=("--all-folders")
    local_nonpersistent_flags+=("--all-folders")
    flags+=("--attachment-name=")
    two_word_flags+=("--attachment-name")
    local_nonpersistent_flags+=("--attachment-name")
    local_nonpersistent_flags+=("--attachment-name=")
    flags+=("--attachment-type=")
    two_word_flags+=("--attachment-type")
    local_nonpersistent_flags+=("--attachment-type")
    local_nonpersistent_flags+=("--attachment-type=")
    flags+=("--before=")
    two_word_flags+=("--before")
    local_nonpersistent_flags+=("--before")
    local_nonpersistent_flags+=("--before=")
    flags+=("--cursor=")
    two_word_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor=")
    flags+=("--flagged")
    local_nonpersistent_flags+=("--flagged")
    flags+=("--folder=")
    two_word_flags+=("--folder")
    flags_with_completion+=("--folder")
    flags_completion+=("__icloud_handle_go_custom_completion")
    local_nonpersistent_flags+=("--folder")
    local_nonpersistent_flags+=("--folder=")
    flags+=("--from=")
    two_word_flags+=("--from")
    local_nonpersistent_flags+=("--from")
    local_nonpersistent_flags+=("--from=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--limit=")
    two_word_flags+=("--limit")
    local_nonpersistent_flags+=("--limit")
    local_nonpersistent_flags+=("--limit=")
    flags+=("--participant=")
    two_word_flags+=("--participant")
    local_nonpersistent_flags+=("--participant")
    local_nonpersistent_flags+=("--participant=")
    flags+=("--read")
    local_nonpersistent_flags+=("--read")
    flags+=("--remote")
    local_nonpersistent_flags+=("--remote")
    flags+=("--result=")
    two_word_flags+=("--result")
    local_nonpersistent_flags+=("--result")
    local_nonpersistent_flags+=("--result=")
    flags+=("--rfc-id=")
    two_word_flags+=("--rfc-id")
    local_nonpersistent_flags+=("--rfc-id")
    local_nonpersistent_flags+=("--rfc-id=")
    flags+=("--subject=")
    two_word_flags+=("--subject")
    local_nonpersistent_flags+=("--subject")
    local_nonpersistent_flags+=("--subject=")
    flags+=("--unflagged")
    local_nonpersistent_flags+=("--unflagged")
    flags+=("--unread")
    local_nonpersistent_flags+=("--unread")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_send()
{
    last_command="icloud_mail_send"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--attach=")
    two_word_flags+=("--attach")
    local_nonpersistent_flags+=("--attach")
    local_nonpersistent_flags+=("--attach=")
    flags+=("--attach-name=")
    two_word_flags+=("--attach-name")
    local_nonpersistent_flags+=("--attach-name")
    local_nonpersistent_flags+=("--attach-name=")
    flags+=("--attach-type=")
    two_word_flags+=("--attach-type")
    local_nonpersistent_flags+=("--attach-type")
    local_nonpersistent_flags+=("--attach-type=")
    flags+=("--bcc=")
    two_word_flags+=("--bcc")
    local_nonpersistent_flags+=("--bcc")
    local_nonpersistent_flags+=("--bcc=")
    flags+=("--body=")
    two_word_flags+=("--body")
    local_nonpersistent_flags+=("--body")
    local_nonpersistent_flags+=("--body=")
    flags+=("--body-file=")
    two_word_flags+=("--body-file")
    local_nonpersistent_flags+=("--body-file")
    local_nonpersistent_flags+=("--body-file=")
    flags+=("--cc=")
    two_word_flags+=("--cc")
    local_nonpersistent_flags+=("--cc")
    local_nonpersistent_flags+=("--cc=")
    flags+=("--dry-run")
    local_nonpersistent_flags+=("--dry-run")
    flags+=("--format=")
    two_word_flags+=("--format")
    local_nonpersistent_flags+=("--format")
    local_nonpersistent_flags+=("--format=")
    flags+=("--from=")
    two_word_flags+=("--from")
    local_nonpersistent_flags+=("--from")
    local_nonpersistent_flags+=("--from=")
    flags+=("--header=")
    two_word_flags+=("--header")
    local_nonpersistent_flags+=("--header")
    local_nonpersistent_flags+=("--header=")
    flags+=("--headers")
    local_nonpersistent_flags+=("--headers")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--html-body=")
    two_word_flags+=("--html-body")
    local_nonpersistent_flags+=("--html-body")
    local_nonpersistent_flags+=("--html-body=")
    flags+=("--html-body-file=")
    two_word_flags+=("--html-body-file")
    local_nonpersistent_flags+=("--html-body-file")
    local_nonpersistent_flags+=("--html-body-file=")
    flags+=("--idempotency-key=")
    two_word_flags+=("--idempotency-key")
    local_nonpersistent_flags+=("--idempotency-key")
    local_nonpersistent_flags+=("--idempotency-key=")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--reply-to=")
    two_word_flags+=("--reply-to")
    local_nonpersistent_flags+=("--reply-to")
    local_nonpersistent_flags+=("--reply-to=")
    flags+=("--retry-of=")
    two_word_flags+=("--retry-of")
    local_nonpersistent_flags+=("--retry-of")
    local_nonpersistent_flags+=("--retry-of=")
    flags+=("--retry-unknown")
    local_nonpersistent_flags+=("--retry-unknown")
    flags+=("--subject=")
    two_word_flags+=("--subject")
    local_nonpersistent_flags+=("--subject")
    local_nonpersistent_flags+=("--subject=")
    flags+=("--to=")
    two_word_flags+=("--to")
    local_nonpersistent_flags+=("--to")
    local_nonpersistent_flags+=("--to=")
    flags+=("--verbose")
    local_nonpersistent_flags+=("--verbose")
    flags+=("--yes")
    local_nonpersistent_flags+=("--yes")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_status()
{
    last_command="icloud_mail_status"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--cursor=")
    two_word_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--limit=")
    two_word_flags+=("--limit")
    local_nonpersistent_flags+=("--limit")
    local_nonpersistent_flags+=("--limit=")
    flags+=("--mutations")
    local_nonpersistent_flags+=("--mutations")
    flags+=("--outbound")
    local_nonpersistent_flags+=("--outbound")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_sync_backfill()
{
    last_command="icloud_mail_sync_backfill"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--after=")
    two_word_flags+=("--after")
    local_nonpersistent_flags+=("--after")
    local_nonpersistent_flags+=("--after=")
    flags+=("--before=")
    two_word_flags+=("--before")
    local_nonpersistent_flags+=("--before")
    local_nonpersistent_flags+=("--before=")
    flags+=("--folder=")
    two_word_flags+=("--folder")
    flags_with_completion+=("--folder")
    flags_completion+=("__icloud_handle_go_custom_completion")
    local_nonpersistent_flags+=("--folder")
    local_nonpersistent_flags+=("--folder=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--limit=")
    two_word_flags+=("--limit")
    local_nonpersistent_flags+=("--limit")
    local_nonpersistent_flags+=("--limit=")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_sync_checkpoints()
{
    last_command="icloud_mail_sync_checkpoints"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--cursor=")
    two_word_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--limit=")
    two_word_flags+=("--limit")
    local_nonpersistent_flags+=("--limit")
    local_nonpersistent_flags+=("--limit=")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_sync_rebaseline()
{
    last_command="icloud_mail_sync_rebaseline"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--current-tail")
    local_nonpersistent_flags+=("--current-tail")
    flags+=("--folder=")
    two_word_flags+=("--folder")
    flags_with_completion+=("--folder")
    flags_completion+=("__icloud_handle_go_custom_completion")
    local_nonpersistent_flags+=("--folder")
    local_nonpersistent_flags+=("--folder=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_sync_repair()
{
    last_command="icloud_mail_sync_repair"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--apply")
    local_nonpersistent_flags+=("--apply")
    flags+=("--check")
    local_nonpersistent_flags+=("--check")
    flags+=("--cleanup-orphans")
    local_nonpersistent_flags+=("--cleanup-orphans")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--limit=")
    two_word_flags+=("--limit")
    local_nonpersistent_flags+=("--limit")
    local_nonpersistent_flags+=("--limit=")
    flags+=("--verbose")
    local_nonpersistent_flags+=("--verbose")
    flags+=("--yes")
    local_nonpersistent_flags+=("--yes")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_sync_status()
{
    last_command="icloud_mail_sync_status"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--folder=")
    two_word_flags+=("--folder")
    local_nonpersistent_flags+=("--folder")
    local_nonpersistent_flags+=("--folder=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_sync()
{
    last_command="icloud_mail_sync"

    command_aliases=()

    commands=()
    commands+=("backfill")
    commands+=("checkpoints")
    commands+=("rebaseline")
    commands+=("repair")
    commands+=("status")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--all-folders")
    local_nonpersistent_flags+=("--all-folders")
    flags+=("--continue-on-error")
    local_nonpersistent_flags+=("--continue-on-error")
    flags+=("--folder=")
    two_word_flags+=("--folder")
    flags_with_completion+=("--folder")
    flags_completion+=("__icloud_handle_go_custom_completion")
    local_nonpersistent_flags+=("--folder")
    local_nonpersistent_flags+=("--folder=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--hydrate=")
    two_word_flags+=("--hydrate")
    local_nonpersistent_flags+=("--hydrate")
    local_nonpersistent_flags+=("--hydrate=")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--jsonl")
    local_nonpersistent_flags+=("--jsonl")
    flags+=("--limit=")
    two_word_flags+=("--limit")
    local_nonpersistent_flags+=("--limit")
    local_nonpersistent_flags+=("--limit=")
    flags+=("--reconcile-outbound")
    local_nonpersistent_flags+=("--reconcile-outbound")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_thread_show()
{
    last_command="icloud_mail_thread_show"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--body-only")
    local_nonpersistent_flags+=("--body-only")
    flags+=("--cursor=")
    two_word_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor=")
    flags+=("--format=")
    two_word_flags+=("--format")
    local_nonpersistent_flags+=("--format")
    local_nonpersistent_flags+=("--format=")
    flags+=("--full")
    local_nonpersistent_flags+=("--full")
    flags+=("--headers")
    local_nonpersistent_flags+=("--headers")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--limit=")
    two_word_flags+=("--limit")
    local_nonpersistent_flags+=("--limit")
    local_nonpersistent_flags+=("--limit=")
    flags+=("--metadata-only")
    local_nonpersistent_flags+=("--metadata-only")
    flags+=("--verbose")
    local_nonpersistent_flags+=("--verbose")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_thread()
{
    last_command="icloud_mail_thread"

    command_aliases=()

    commands=()
    commands+=("show")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_threads_list()
{
    last_command="icloud_mail_threads_list"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--after=")
    two_word_flags+=("--after")
    local_nonpersistent_flags+=("--after")
    local_nonpersistent_flags+=("--after=")
    flags+=("--attachment-name=")
    two_word_flags+=("--attachment-name")
    local_nonpersistent_flags+=("--attachment-name")
    local_nonpersistent_flags+=("--attachment-name=")
    flags+=("--attachment-type=")
    two_word_flags+=("--attachment-type")
    local_nonpersistent_flags+=("--attachment-type")
    local_nonpersistent_flags+=("--attachment-type=")
    flags+=("--before=")
    two_word_flags+=("--before")
    local_nonpersistent_flags+=("--before")
    local_nonpersistent_flags+=("--before=")
    flags+=("--cursor=")
    two_word_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor=")
    flags+=("--flagged")
    local_nonpersistent_flags+=("--flagged")
    flags+=("--folder=")
    two_word_flags+=("--folder")
    local_nonpersistent_flags+=("--folder")
    local_nonpersistent_flags+=("--folder=")
    flags+=("--from=")
    two_word_flags+=("--from")
    local_nonpersistent_flags+=("--from")
    local_nonpersistent_flags+=("--from=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--limit=")
    two_word_flags+=("--limit")
    local_nonpersistent_flags+=("--limit")
    local_nonpersistent_flags+=("--limit=")
    flags+=("--participant=")
    two_word_flags+=("--participant")
    local_nonpersistent_flags+=("--participant")
    local_nonpersistent_flags+=("--participant=")
    flags+=("--query=")
    two_word_flags+=("--query")
    local_nonpersistent_flags+=("--query")
    local_nonpersistent_flags+=("--query=")
    flags+=("--read")
    local_nonpersistent_flags+=("--read")
    flags+=("--rfc-id=")
    two_word_flags+=("--rfc-id")
    local_nonpersistent_flags+=("--rfc-id")
    local_nonpersistent_flags+=("--rfc-id=")
    flags+=("--subject=")
    two_word_flags+=("--subject")
    local_nonpersistent_flags+=("--subject")
    local_nonpersistent_flags+=("--subject=")
    flags+=("--unflagged")
    local_nonpersistent_flags+=("--unflagged")
    flags+=("--unread")
    local_nonpersistent_flags+=("--unread")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_threads_show()
{
    last_command="icloud_mail_threads_show"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--body-only")
    local_nonpersistent_flags+=("--body-only")
    flags+=("--cursor=")
    two_word_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor=")
    flags+=("--format=")
    two_word_flags+=("--format")
    local_nonpersistent_flags+=("--format")
    local_nonpersistent_flags+=("--format=")
    flags+=("--full")
    local_nonpersistent_flags+=("--full")
    flags+=("--headers")
    local_nonpersistent_flags+=("--headers")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--limit=")
    two_word_flags+=("--limit")
    local_nonpersistent_flags+=("--limit")
    local_nonpersistent_flags+=("--limit=")
    flags+=("--metadata-only")
    local_nonpersistent_flags+=("--metadata-only")
    flags+=("--verbose")
    local_nonpersistent_flags+=("--verbose")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_threads()
{
    last_command="icloud_mail_threads"

    command_aliases=()

    commands=()
    commands+=("list")
    commands+=("show")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_trash()
{
    last_command="icloud_mail_trash"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry-run")
    local_nonpersistent_flags+=("--dry-run")
    flags+=("--from=")
    two_word_flags+=("--from")
    flags_with_completion+=("--from")
    flags_completion+=("__icloud_handle_go_custom_completion")
    local_nonpersistent_flags+=("--from")
    local_nonpersistent_flags+=("--from=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--idempotency-key=")
    two_word_flags+=("--idempotency-key")
    local_nonpersistent_flags+=("--idempotency-key")
    local_nonpersistent_flags+=("--idempotency-key=")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--to=")
    two_word_flags+=("--to")
    local_nonpersistent_flags+=("--to")
    local_nonpersistent_flags+=("--to=")
    flags+=("--yes")
    local_nonpersistent_flags+=("--yes")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_unarchive()
{
    last_command="icloud_mail_unarchive"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry-run")
    local_nonpersistent_flags+=("--dry-run")
    flags+=("--from=")
    two_word_flags+=("--from")
    flags_with_completion+=("--from")
    flags_completion+=("__icloud_handle_go_custom_completion")
    local_nonpersistent_flags+=("--from")
    local_nonpersistent_flags+=("--from=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--idempotency-key=")
    two_word_flags+=("--idempotency-key")
    local_nonpersistent_flags+=("--idempotency-key")
    local_nonpersistent_flags+=("--idempotency-key=")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--to=")
    two_word_flags+=("--to")
    local_nonpersistent_flags+=("--to")
    local_nonpersistent_flags+=("--to=")
    flags+=("--yes")
    local_nonpersistent_flags+=("--yes")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_unflag()
{
    last_command="icloud_mail_unflag"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--folder=")
    two_word_flags+=("--folder")
    flags_with_completion+=("--folder")
    flags_completion+=("__icloud_handle_go_custom_completion")
    local_nonpersistent_flags+=("--folder")
    local_nonpersistent_flags+=("--folder=")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--input-json=")
    two_word_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json")
    local_nonpersistent_flags+=("--input-json=")
    flags+=("--input-jsonl=")
    two_word_flags+=("--input-jsonl")
    local_nonpersistent_flags+=("--input-jsonl")
    local_nonpersistent_flags+=("--input-jsonl=")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--jsonl")
    local_nonpersistent_flags+=("--jsonl")
    flags+=("--mode=")
    two_word_flags+=("--mode")
    local_nonpersistent_flags+=("--mode")
    local_nonpersistent_flags+=("--mode=")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail_watch()
{
    last_command="icloud_mail_watch"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--all-accounts")
    local_nonpersistent_flags+=("--all-accounts")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--jsonl")
    local_nonpersistent_flags+=("--jsonl")
    flags+=("--limit=")
    two_word_flags+=("--limit")
    local_nonpersistent_flags+=("--limit")
    local_nonpersistent_flags+=("--limit=")
    flags+=("--poll-interval=")
    two_word_flags+=("--poll-interval")
    local_nonpersistent_flags+=("--poll-interval")
    local_nonpersistent_flags+=("--poll-interval=")
    flags+=("--reconcile-interval=")
    two_word_flags+=("--reconcile-interval")
    local_nonpersistent_flags+=("--reconcile-interval")
    local_nonpersistent_flags+=("--reconcile-interval=")
    flags+=("--verbose")
    local_nonpersistent_flags+=("--verbose")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_mail()
{
    last_command="icloud_mail"

    command_aliases=()

    commands=()
    commands+=("archive")
    commands+=("attachments")
    commands+=("copy")
    commands+=("delete")
    commands+=("draft")
    commands+=("flag")
    commands+=("folders")
    commands+=("forward")
    commands+=("identities")
    commands+=("inbox")
    commands+=("latest")
    commands+=("mark-read")
    commands+=("mark-unread")
    commands+=("message")
    commands+=("messages")
    commands+=("move")
    commands+=("operations")
    commands+=("reply")
    commands+=("reply-all")
    commands+=("restore")
    commands+=("search")
    commands+=("send")
    commands+=("status")
    commands+=("sync")
    commands+=("thread")
    commands+=("threads")
    commands+=("trash")
    commands+=("unarchive")
    commands+=("unflag")
    commands+=("watch")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_man()
{
    last_command="icloud_man"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--output-dir=")
    two_word_flags+=("--output-dir")
    local_nonpersistent_flags+=("--output-dir")
    local_nonpersistent_flags+=("--output-dir=")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_version()
{
    last_command="icloud_version"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--json")
    local_nonpersistent_flags+=("--json")
    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_icloud_root_command()
{
    last_command="icloud"

    command_aliases=()

    commands=()
    commands+=("account")
    commands+=("capabilities")
    commands+=("completion")
    commands+=("doctor")
    commands+=("help")
    commands+=("mail")
    commands+=("man")
    commands+=("version")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--account=")
    two_word_flags+=("--account")
    flags_with_completion+=("--account")
    flags_completion+=("__icloud_handle_go_custom_completion")
    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--home=")
    two_word_flags+=("--home")
    flags+=("--no-input")
    flags+=("--request-id=")
    two_word_flags+=("--request-id")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")
    flags+=("--version")
    flags+=("-v")
    local_nonpersistent_flags+=("--version")
    local_nonpersistent_flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

__start_icloud()
{
    local cur prev words cword split
    declare -A flaghash 2>/dev/null || :
    declare -A aliashash 2>/dev/null || :
    if declare -F _init_completion >/dev/null 2>&1; then
        _init_completion -s || return
    else
        __icloud_init_completion -n "=" || return
    fi

    local c=0
    local flag_parsing_disabled=
    local flags=()
    local two_word_flags=()
    local local_nonpersistent_flags=()
    local flags_with_completion=()
    local flags_completion=()
    local commands=("icloud")
    local command_aliases=()
    local must_have_one_flag=()
    local must_have_one_noun=()
    local has_completion_function=""
    local last_command=""
    local nouns=()
    local noun_aliases=()

    __icloud_handle_word
}

if [[ $(type -t compopt) = "builtin" ]]; then
    complete -o default -F __start_icloud icloud
else
    complete -o default -o nospace -F __start_icloud icloud
fi

# ex: ts=4 sw=4 et filetype=sh
