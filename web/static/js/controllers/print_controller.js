import { Controller } from "@hotwired/stimulus"

// Opens the browser's print dialog (which can also save a PDF).
//   <button type="button" data-controller="print" data-action="print#print">
export default class extends Controller {
  print() {
    window.print()
  }
}
