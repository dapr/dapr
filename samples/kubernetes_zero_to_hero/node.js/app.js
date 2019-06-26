const express = require('express')
var bodyParser = require('body-parser')
const app = express()
app.use(bodyParser.json())

const port = 3000

app.post('/state', (req, res) => {
    e = req.body

    if (e.length > 0) {
        order = e[0].value
    }

    res.status(200).send()
})

var order;

app.get('/order', (req, res) => {
    res.json(order)
})

app.post('/neworder', (req, res) => {
    data = req.body.data
    orderID = data.orderID

    console.log("Got a new order! Order ID: " + orderID)

    order = data
    
    res.json({
        state: [
            {
                key: "order",
                value: order
            }
        ]
    })
})

app.listen(port, () => console.log(`Node App listening on port ${port}!`))
