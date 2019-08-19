import React from 'react';
import { Navbar } from 'react-bootstrap'

export function Nav(){
    return(
    <Navbar bg="dark" variant="dark">
        <Navbar.Brand href="#home">
          {' Pubsub Sample'}
        </Navbar.Brand>
      </Navbar>
    )
}