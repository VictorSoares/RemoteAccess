Add-Type -AssemblyName System.Drawing
$masterPath = "d:\Documentos\GitHub\RemoteAccess\winres\icon.png"
$master = [System.Drawing.Image]::FromFile($masterPath)
$sizes = @(16, 20, 24, 32, 40, 48, 64, 96, 128, 256)

foreach ($s in $sizes) {
    $bmp = New-Object System.Drawing.Bitmap $s, $s, ([System.Drawing.Imaging.PixelFormat]::Format32bppArgb)
    $g = [System.Drawing.Graphics]::FromImage($bmp)
    $g.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
    $g.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::HighQuality
    $g.PixelOffsetMode = [System.Drawing.Drawing2D.PixelOffsetMode]::HighQuality
    $g.CompositingQuality = [System.Drawing.Drawing2D.CompositingQuality]::HighQuality
    $g.Clear([System.Drawing.Color]::Transparent)
    $g.DrawImage($master, (New-Object System.Drawing.Rectangle 0, 0, $s, $s))
    $g.Dispose()
    
    $outPath = "d:\Documentos\GitHub\RemoteAccess\winres\icon_$s.png"
    $bmp.Save($outPath, [System.Drawing.Imaging.ImageFormat]::Png)
    $bmp.Dispose()
    Write-Host "Generated: icon_$s.png"
}
$master.Dispose()
