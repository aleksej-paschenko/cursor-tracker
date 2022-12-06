
# What this project is about

This is a simple web server that exposes an index page. 
When you open the page, you will see circles of different colors. 
Don't be scary. Each circle is a mouse position of somebody, who is looking at the same page at the moment.
Ah, and there are a few bots.

# How to use it

There is a Makefile in the root folder, it contains several commands to make your life a bit easier.
The most important are:
* `make run` runs the web server. By default, the server will open 4567 port, and you can access it at [localhost:4567](`http://localhost:4567`).
* `make test` runs a test suite.

# Implementation details

It is a simple web server, it is not designed to handle millions of clients working simultaneously. 

A list of possible improvements:
* **Using [epoll](https://man7.org/linux/man-pages/man7/epoll.7.html) system call to decrease amount of goroutines.**
The current implementation handles each websocket connection in a dedicated goroutine. This can be a problem for scaling to 1M clients.
Inspired by [1m-go-websockets](https://github.com/eranyanay/1m-go-websockets/tree/master/3_optimize_ws_goroutines).
* **Optimize the amount of messages between the server and clients**. 
Now, when the server gets a new mouse position of a client, it sends the position immediately to all other clients. 
This approach leads to a huge number of messages between the server and browsers and doesn't scale well. 
The idea is either to send new coordinates to clients in a batch or to update a client's mouse position only if the change is greater than some threshold. 


