# Actions and Node.js

The following example shows a simple node app that does the following:

1. Listens for events from an Event Source named MyEventSource
2. Publishes an event to another action named action-2
3. Registers to get all saved states when the process launches

```
const express = require('express')
var bodyParser = require('body-parser')
var request = require('request')
const app = express()
app.use(bodyParser.json())

var order;

// restore state when process launches
app.post('/state', (req, res) => {
    e = req.body
    order = e[0].value
    res.status(200).send()
})

// listen to events from myeventsource
app.post('/myeventsource', (req, res) => {
    data = req.body.data
    console.log(data)

    publish({
        to: [
            'action-2'
        ],
        data: {
            value: 5
        }
    })
})

function publish(event) {
    var options = {
        uri: 'localhost:3500/publish',
        method: 'POST',
        json: event
    };

    request(options, function (error, response, body) {
        if (!error && response.statusCode == 200) {
            console.log("success!")
        }
    });
}

app.listen(3000, () => console.log(`App listening on port 3000!`))

```