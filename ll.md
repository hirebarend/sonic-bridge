I want you to review this project as a senior principal engineer and help me polish it.

The goal of this project is to have a `console`, `esp32`, or `web` version that can act as the source for the audio stream. Then have a `server` which captures the source stream and streams it back to any connected destination. The destination we want should also be part of the `web`. Since the `web` project doesn't exist, you should create it as a react project using the vite CLI and tailwind. The interface should be extremely simply but still look good.

I want you to also reference the `/Users/barenderasmus/development/examples/core/docs/CODING_STANDARDS.md` as coding standard so that you have an idea of what I expect in terms of standards.

I expect your workflow to be as follow and it should be executed step by step. Keep in mind that you should first create a plan once you have an overview and then execute the individual steps.

- Review and analyse this project so that you understand it's current state
- Refine the project, to be more readable, align with the coding standards and follow a similar approach. You can also convert the `console` project to a `Go` project instead of C++
- Start creating the `web` project using the vite CLI first, then adding tailwindcss before you start building the interface. It needs to be mobile friendly. Keep in mind that you should think about wakelock functionality and other useful functionlity to include.
- I want you to review all the projects, especially all the Go projects and make sure they only have the needed abstractions and follow a logical top-down approach.
- For all `sources`, i.e. console and web, I want them to have a similar flow so that I can relate and compare. They should also have compression such as mu-law or other that can be slotted into the pipeline.
- For the `web` destination, I want you to use a video or audio player as the stream.
- Lastly, I want you to look at a deployment setup similar to this, `/Users/barenderasmus/development/examples/core/scripts` where I can deploy everything onto a Digital Ocean droplet with a domain name, SSL, etc


---

I want you to review the complexity of this project and help me simplify it. You should keep the abstractions which make sense but remove those that are overkill and not required. You can also simplify the command arguments as most of them have not be requested and wont be used. 

Lastly, you should also validate if we can remove the web project and just serve a `stream.wav` as live audio from the server over https. 


--- 

I want you to further simplify it. If possible keep the mu-law encoding on the source side and for the destination, is very important that we support iPhone and therefor the MP3 version. The code in between should be understandable and simplistic rather than overly optimized, futureproofed and every edge case covered.

The primary objective of this project is to allow the `console`/`esp32` to stream to the server and connect with a iPhone over the browser and receive the audio. Latency doesn't matter that much, quality needs to be good enough, etc. Relook at this project and help further simplify it.