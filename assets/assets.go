package assets

import "embed"

//go:embed fonts/JetBrainsMono-Regular.ttf
var MonoFont []byte

//go:embed fonts/Inter-Regular.otf
var InterfaceFont []byte

//go:embed fonts/Inter-SemiBold.otf
var InterfaceSemiboldFont []byte

//go:embed icons/datacraft-gorex-orange.png
var AppIcon []byte

//go:embed icons/*.svg
var ControlIcons embed.FS
