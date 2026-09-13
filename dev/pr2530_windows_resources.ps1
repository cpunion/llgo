$ErrorActionPreference = 'Continue'
while ($true) {
    try {
        $os = Get-CimInstance Win32_OperatingSystem
        $processes = @(Get-Process)
        $top = @($processes | Sort-Object WorkingSet64 -Descending | Select-Object -First 12 Name, Id, WorkingSet64, PrivateMemorySize64, HandleCount, @{n='Threads';e={$_.Threads.Count}})
        $sample = [ordered]@{
            utc = [DateTime]::UtcNow.ToString('o')
            totalPhysicalMiB = [math]::Round($os.TotalVisibleMemorySize / 1024)
            freePhysicalMiB = [math]::Round($os.FreePhysicalMemory / 1024)
            totalVirtualMiB = [math]::Round($os.TotalVirtualMemorySize / 1024)
            freeVirtualMiB = [math]::Round($os.FreeVirtualMemory / 1024)
            processCount = $processes.Count
            top = $top
        }
        Write-Output ('RESOURCE ' + ($sample | ConvertTo-Json -Compress -Depth 4))
    } catch {
        Write-Output ('RESOURCE-ERROR ' + $_.Exception.Message)
    }
    Start-Sleep -Seconds 15
}
