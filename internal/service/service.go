// Package service registers CarryOn as a Windows Scheduled Task at logon.
package service

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// TaskName is the scheduled task CarryOn manages.
const TaskName = "CarryOn"

// Script builds the PowerShell that registers the preference task. S4U runs
// without an interactive session, so no console window appears.
func Script(exePath, workDir, user string) string {
	return fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$action = New-ScheduledTaskAction -Execute '%s' -Argument 'watch' -WorkingDirectory '%s'
$trigger = New-ScheduledTaskTrigger -AtLogOn -User '%s'
$principal = New-ScheduledTaskPrincipal -UserId '%s' -LogonType S4U -RunLevel Limited
$settings = New-ScheduledTaskSettingsSet -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) -MultipleInstances IgnoreNew -ExecutionTimeLimit ([TimeSpan]::Zero) -StartWhenAvailable -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries
Register-ScheduledTask -TaskName '%s' -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Description 'CarryOn keeps Claude Code and Codex work going through usage limits.' -Force | Out-Null`,
		escape(exePath), escape(workDir), escape(user), escape(user), TaskName)
}

// InteractiveScript is the fallback when S4U is unavailable. It may show a
// console window at logon.
func InteractiveScript(exePath, workDir, user string) string {
	return fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$action = New-ScheduledTaskAction -Execute '%s' -Argument 'watch' -WorkingDirectory '%s'
$trigger = New-ScheduledTaskTrigger -AtLogOn -User '%s'
$settings = New-ScheduledTaskSettingsSet -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) -MultipleInstances IgnoreNew -ExecutionTimeLimit ([TimeSpan]::Zero) -StartWhenAvailable -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries
Register-ScheduledTask -TaskName '%s' -Action $action -Trigger $trigger -Settings $settings -Description 'CarryOn keeps Claude Code and Codex work going through usage limits.' -Force | Out-Null`,
		escape(exePath), escape(workDir), escape(user), TaskName)
}

// escape makes a value safe inside a single-quoted PowerShell string.
func escape(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

// Install registers the task for the current user.
// Install registers the task, preferring S4U so no console window opens. It
// returns a note describing which mode was used.
func Install(ctx context.Context, exePath, workDir string) (string, error) {
	if exePath == "" || workDir == "" {
		return "", errors.New("install needs an executable and a working directory")
	}
	user, err := currentUser(ctx)
	if err != nil {
		return "", err
	}
	if err := powershell(ctx, Script(exePath, workDir, user)); err == nil {
		return "Installed the CarryOn task with S4U; it runs without a console window.", nil
	}
	if err := powershell(ctx, InteractiveScript(exePath, workDir, user)); err != nil {
		return "", err
	}
	return "Installed the interactive logon task. S4U needs admin rights or a log-on-as-batch-job policy, so Windows may show a console window.", nil
}

// Uninstall removes the task, ignoring an already-absent task.
func Uninstall(ctx context.Context) error {
	script := fmt.Sprintf(`$ErrorActionPreference = 'SilentlyContinue'
Unregister-ScheduledTask -TaskName '%s' -Confirm:$false`, TaskName)
	return powershell(ctx, script)
}

// Status reports whether the task exists and its last result.
func Status(ctx context.Context) (string, error) {
	script := fmt.Sprintf(`$task = Get-ScheduledTask -TaskName '%s' -ErrorAction SilentlyContinue
if ($null -eq $task) { 'not installed'; exit 0 }
$info = Get-ScheduledTaskInfo -TaskName '%s'
"state: " + $task.State
"last run: " + $info.LastRunTime
"last result: " + $info.LastTaskResult`, TaskName, TaskName)
	return powershellOutput(ctx, script)
}

func currentUser(ctx context.Context) (string, error) {
	output, err := powershellOutput(ctx, `[System.Security.Principal.WindowsIdentity]::GetCurrent().Name`)
	if err != nil {
		return "", err
	}
	user := strings.TrimSpace(output)
	if user == "" {
		return "", errors.New("could not read the current Windows user")
	}
	return user, nil
}

func powershell(ctx context.Context, script string) error {
	_, err := powershellOutput(ctx, script)
	return err
}

func powershellOutput(ctx context.Context, script string) (string, error) {
	command := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("scheduled task command failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}
