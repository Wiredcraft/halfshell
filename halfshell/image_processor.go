// Copyright (c) 2014 Oyster
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package halfshell

import (
	"fmt"
	"math"
	"strings"

	"github.com/rafikk/imagick/imagick"
)

// ImageProcessor is the public interface for the image processor. It exposes a
// single method to process an image with options.
type ImageProcessor interface {
	ProcessImage(*Image, *ImageProcessorOptions) *Image
}

// ImageProcessorOptions specify the request parameters for the processing
// operation.
type ImageProcessorOptions struct {
	Dimensions ImageDimensions
	BlurRadius float64
}

type imageProcessor struct {
	Config *ProcessorConfig
	Logger *Logger
}

// NewImageProcessorWithConfig creates a new ImageProcessor instance using
// configuration settings.
func NewImageProcessorWithConfig(config *ProcessorConfig) ImageProcessor {
	return &imageProcessor{
		Config: config,
		Logger: NewLogger("image_processor.%s", config.Name),
	}
}

// The public method for processing an image. The method receives an original
// image and options and returns the processed image.
func (ip *imageProcessor) ProcessImage(image *Image, request *ImageProcessorOptions) *Image {
	processedImage := Image{}
	wand := imagick.NewMagickWand()
	defer wand.Destroy()

	wand.ReadImageBlob(image.Bytes)
	scaleModified, err := ip.scaleWand(wand, request)
	if err != nil {
		ip.Logger.Warnf("Error scaling image: %s", err)
		return nil
	}

	blurModified, err := ip.blurWand(wand, request)
	if err != nil {
		ip.Logger.Warnf("Error blurring image: %s", err)
		return nil
	}

	if !scaleModified && !blurModified {
		processedImage.Bytes = image.Bytes
	} else {
		processedImage.Bytes = wand.GetImageBlob()
	}

	processedImage.Signature = wand.GetImageSignature()
	processedImage.MimeType = fmt.Sprintf("image/%s", strings.ToLower(wand.GetImageFormat()))

	return &processedImage
}

func (ip *imageProcessor) scaleWand(wand *imagick.MagickWand, request *ImageProcessorOptions) (modified bool, err error) {
	currentDimensions := ImageDimensions{uint64(wand.GetImageWidth()), uint64(wand.GetImageHeight())}
	newDimensions := ip.getScaledDimensions(currentDimensions, request)

	if newDimensions == currentDimensions {
		return false, nil
	}

	if err = wand.ResizeImage(uint(newDimensions.Width), uint(newDimensions.Height), imagick.FILTER_LANCZOS, 1); err != nil {
		ip.Logger.Warnf("ImageMagick error resizing image: %s", err)
		return true, err
	}

	if err = wand.SetImageInterpolateMethod(imagick.INTERPOLATE_PIXEL_BICUBIC); err != nil {
		ip.Logger.Warnf("ImageMagick error setting interpoliation method: %s", err)
		return true, err
	}

	if err = wand.StripImage(); err != nil {
		ip.Logger.Warnf("ImageMagick error stripping image routes and metadata")
		return true, err
	}

	if "JPEG" == wand.GetImageFormat() {
		if err = wand.SetImageInterlaceScheme(imagick.INTERLACE_PLANE); err != nil {
			ip.Logger.Warnf("ImageMagick error setting the image interlace scheme")
			return true, err
		}

		if err = wand.SetImageCompression(imagick.COMPRESSION_JPEG); err != nil {
			ip.Logger.Warnf("ImageMagick error setting the image compression type")
			return true, err
		}

		if err = wand.SetImageCompressionQuality(uint(ip.Config.ImageCompressionQuality)); err != nil {
			ip.Logger.Warnf("sImageMagick error setting compression quality: %s", err)
			return true, err
		}
	}

	return true, nil
}

func (ip *imageProcessor) blurWand(wand *imagick.MagickWand, request *ImageProcessorOptions) (modified bool, err error) {
	if request.BlurRadius != 0 {
		blurRadius := float64(wand.GetImageWidth()) * request.BlurRadius * ip.Config.MaxBlurRadiusPercentage
		if err = wand.GaussianBlurImage(blurRadius, blurRadius); err != nil {
			ip.Logger.Warnf("ImageMagick error setting blur radius: %s", err)
		}
		return true, err
	}
	return false, nil
}

func (ip *imageProcessor) getScaledDimensions(currentDimensions ImageDimensions, request *ImageProcessorOptions) ImageDimensions {
	requestDimensions := request.Dimensions
	if requestDimensions.Width == 0 && requestDimensions.Height == 0 {
		requestDimensions = ImageDimensions{Width: ip.Config.DefaultImageWidth, Height: ip.Config.DefaultImageHeight}
	}

	dimensions := ip.scaleToRequestedDimensions(currentDimensions, requestDimensions, request)
	return ip.clampDimensionsToMaxima(dimensions, request)
}

func (ip *imageProcessor) scaleToRequestedDimensions(currentDimensions, requestedDimensions ImageDimensions, request *ImageProcessorOptions) ImageDimensions {
	imageAspectRatio := currentDimensions.AspectRatio()
	if requestedDimensions.Width > 0 && requestedDimensions.Height > 0 {
		requestedAspectRatio := requestedDimensions.AspectRatio()
		ip.Logger.Infof("Requested image ratio %f, image ratio %f, %v", requestedAspectRatio, imageAspectRatio, ip.Config.MaintainAspectRatio)

		if !ip.Config.MaintainAspectRatio {
			// If we're not asked to maintain the aspect ratio, give them what they want
			return requestedDimensions
		}

		if requestedAspectRatio > imageAspectRatio {
			// The requested aspect ratio is wider than the image's natural ratio.
			// Thus means the height is the restraining dimension, so unset the
			// width and let the height determine the dimensions.
			return ip.scaleToRequestedDimensions(currentDimensions, ImageDimensions{0, requestedDimensions.Height}, request)
		} else if requestedAspectRatio < imageAspectRatio {
			return ip.scaleToRequestedDimensions(currentDimensions, ImageDimensions{requestedDimensions.Width, 0}, request)
		} else {
			return requestedDimensions
		}
	}

	if requestedDimensions.Width > 0 {
		return ImageDimensions{requestedDimensions.Width, ip.getAspectScaledHeight(imageAspectRatio, requestedDimensions.Width)}
	}

	if requestedDimensions.Height > 0 {
		return ImageDimensions{ip.getAspectScaledWidth(imageAspectRatio, requestedDimensions.Height), requestedDimensions.Height}
	}

	return currentDimensions
}

func (ip *imageProcessor) clampDimensionsToMaxima(dimensions ImageDimensions, request *ImageProcessorOptions) ImageDimensions {
	if ip.Config.MaxImageWidth > 0 && dimensions.Width > ip.Config.MaxImageWidth {
		scaledHeight := ip.getAspectScaledHeight(dimensions.AspectRatio(), ip.Config.MaxImageWidth)
		return ip.clampDimensionsToMaxima(ImageDimensions{ip.Config.MaxImageWidth, scaledHeight}, request)
	}

	if ip.Config.MaxImageHeight > 0 && dimensions.Height > ip.Config.MaxImageHeight {
		scaledWidth := ip.getAspectScaledWidth(dimensions.AspectRatio(), ip.Config.MaxImageHeight)
		return ip.clampDimensionsToMaxima(ImageDimensions{scaledWidth, ip.Config.MaxImageHeight}, request)
	}

	return dimensions
}

func (ip *imageProcessor) getAspectScaledHeight(aspectRatio float64, width uint64) uint64 {
	return uint64(math.Floor(float64(width)/aspectRatio + 0.5))
}

func (ip *imageProcessor) getAspectScaledWidth(aspectRatio float64, height uint64) uint64 {
	return uint64(math.Floor(float64(height)*aspectRatio + 0.5))
}
