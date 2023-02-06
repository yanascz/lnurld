package main

import (
	"bytes"
	_ "embed"
	"github.com/skip2/go-qrcode"
	"golang.org/x/image/draw"
	"image"
	_ "image/jpeg"
	"image/png"
)

//go:embed files/lightning.png
var lightningPngData []byte

func encodeQrCode(content string, thumbnail *Thumbnail, size int) ([]byte, error) {
	qrCode, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		return nil, err
	}

	thumbnailData := lightningPngData
	if thumbnail != nil {
		thumbnailData = thumbnail.bytes
	}

	thumbnailImage, _, err := image.Decode(bytes.NewReader(thumbnailData))
	if err != nil {
		return nil, err
	}

	qrCodeImage := qrCode.Image(size)
	qrCodeBounds := qrCodeImage.Bounds()
	thumbnailBounds := thumbnailImage.Bounds()

	thumbnailSize := thumbnailBounds.Size()
	thumbnailDestSize := qrCodeBounds.Size().Div(5)
	if thumbnailSize.X < thumbnailSize.Y {
		thumbnailDestSize.X = thumbnailSize.X * thumbnailDestSize.Y / thumbnailSize.Y
	} else if thumbnailSize.X > thumbnailSize.Y {
		thumbnailDestSize.Y = thumbnailSize.Y * thumbnailDestSize.X / thumbnailSize.X
	}

	thumbnailOffset := qrCodeBounds.Size().Sub(thumbnailDestSize).Div(2)
	thumbnailDestRect := image.Rectangle{Min: thumbnailOffset, Max: thumbnailOffset.Add(thumbnailDestSize)}

	rgbaImage := image.NewRGBA(qrCodeBounds)
	draw.Draw(rgbaImage, qrCodeBounds, qrCodeImage, image.Point{}, draw.Over)
	draw.CatmullRom.Scale(rgbaImage, thumbnailDestRect, thumbnailImage, thumbnailBounds, draw.Over, nil)

	var qrCodePngData bytes.Buffer
	pngEncoder := png.Encoder{CompressionLevel: png.BestCompression}
	err = pngEncoder.Encode(&qrCodePngData, rgbaImage)
	if err != nil {
		return nil, err
	}

	return qrCodePngData.Bytes(), nil
}
