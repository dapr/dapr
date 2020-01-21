# Generating PowerShell Completions For Your Own cobra.Command

Cobra can generate PowerShell completion scripts. Users need PowerShell version 5.0 or above, which comes with Windows 10 and can be downloaded separately for Windows 7 or 8.1. They can then write the completions to a file and source this file from their PowerShell profile, which is referenced by the `$Profile` environment variable. See `Get-Help about_Profiles` for more info about PowerShell profiles.

# What's supported

- Completion for subcommands using their `.Short` description
- Completion for non-hidden flags using their `.Name` and `.Shorthand`

# What's not yet supported

- Command aliases
- Required, filename or custom flags (they will work like normal flags)
- Custom completion scripts
