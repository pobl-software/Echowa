// formatCommand writes an app's args as the command line the server splits
// back into the same args (rules.SplitCommand): plain where it can be,
// single-quoted where it can't.
export function formatCommand(args: string[] | null): string {
    return (args ?? [])
        .map((arg) => (/^[\w@%+=:,./-]+$/.test(arg) ? arg : `'${arg.replaceAll("'", `'\\''`)}'`))
        .join(' ');
}
