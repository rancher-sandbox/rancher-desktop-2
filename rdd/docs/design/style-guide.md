# Documentation Style Guide

## Language

Present tense: "Writes to config file". **Not** "Will write to config file".

Simple enumeration: "Does this. Does that." **Not** "First does this. Then does that."

Always use default RDD instance: "The `~/.rd2/bin` directory". **Not** "The `~/.rd$RDD_INSTANCE/bin` directory"

## Terminology

* "API groups" are \`rdd.rancherdesktop.io\`. Always lowercase, fully qualified, and using backquotes, not rdd, \`rdd\` or \`RDD.rancherdesktop.io\`.
* "Object types" are \`App\`, or \`LimaVM\`. They are mixed case and always using backquotes, not App, \`app\` or \`limavm\`.
* Use "control plane" (or "RDD") and not "daemon".

### Instances

* "RDD instance" is a specific control plane, `RDD_INSTANCE=2`
* "Lima instance" is a specific VM (unique within an RDD instance), e.g. `rd`
* "App instance" is the `App` object. There can be only one per RDD instance.
* "Resource instance" is a specific `Resource` object.

### Resources

* "Control plane resources" are called "objects".
* "Application resources" are `Resource` objects.

## Formatting

* No backquotes in hyperlinks. Use "[rdd run]\(…) command". **Not** "[\`rdd run\`]\(…) command".
* Always put quotes around "Rancher Desktop 2".
* Lowercase (and using backquotes) \`rdd\` is the binary executable. RDD (no quotes) is application name.
