import { Controller } from "@hotwired/stimulus"

// Shrinks a picked picture before it is uploaded: draws it onto a canvas
// (honouring the camera's rotation), scales it to 1200 px on the longest
// side, and swaps the file input's file for a JPEG. Also shows a preview.
// Without JavaScript the form still posts the original file and the
// server does the same work.
//   <form data-controller="photo">
//     <img data-photo-target="preview" hidden>
//     <input type="file" data-photo-target="input" data-action="change->photo#pick">
//     <p data-photo-target="status"></p>
export default class extends Controller {
  static targets = ["input", "preview", "empty", "status", "submit"]
  static values = { max: { type: Number, default: 1200 } }

  disconnect() {
    this.revoke()
  }

  async pick() {
    const file = this.inputTarget.files[0]
    if (!file) return
    this.say("")
    this.submitTarget.disabled = true
    try {
      const blob = await this.shrink(file)
      const jpeg = new File([blob], "photo.jpg", { type: "image/jpeg", lastModified: Date.now() })
      const transfer = new DataTransfer()
      transfer.items.add(jpeg)
      this.inputTarget.files = transfer.files
      this.show(blob)
    } catch (error) {
      // The browser can't decode it. Leave the file alone: the server
      // accepts JPEG and PNG and says so if it can't use this one.
      this.say("Your browser couldn't prepare this picture, so it will be sent as it is.")
    } finally {
      this.submitTarget.disabled = false
    }
  }

  async shrink(file) {
    const source = await this.decode(file)
    const width = source.naturalWidth || source.width
    const height = source.naturalHeight || source.height
    const scale = Math.min(1, this.maxValue / Math.max(width, height))
    const canvas = document.createElement("canvas")
    canvas.width = Math.max(1, Math.round(width * scale))
    canvas.height = Math.max(1, Math.round(height * scale))
    const context = canvas.getContext("2d")
    context.fillStyle = "#fff" // JPEG has no transparency
    context.fillRect(0, 0, canvas.width, canvas.height)
    context.drawImage(source, 0, 0, canvas.width, canvas.height)
    if (source.close) source.close()
    return new Promise((resolve, reject) => {
      canvas.toBlob((blob) => (blob ? resolve(blob) : reject(new Error("no blob"))), "image/jpeg", 0.85)
    })
  }

  async decode(file) {
    if (window.createImageBitmap) {
      try {
        return await createImageBitmap(file, { imageOrientation: "from-image" })
      } catch (error) {
        // fall through to an <img>
      }
    }
    const url = URL.createObjectURL(file)
    try {
      const image = new Image()
      image.src = url
      await image.decode()
      return image
    } finally {
      URL.revokeObjectURL(url)
    }
  }

  show(blob) {
    this.revoke()
    this.previewUrl = URL.createObjectURL(blob)
    this.previewTarget.src = this.previewUrl
    this.previewTarget.hidden = false
    if (this.hasEmptyTarget) this.emptyTarget.hidden = true
  }

  revoke() {
    if (this.previewUrl) URL.revokeObjectURL(this.previewUrl)
    this.previewUrl = null
  }

  say(text) {
    if (this.hasStatusTarget) this.statusTarget.textContent = text
  }
}
